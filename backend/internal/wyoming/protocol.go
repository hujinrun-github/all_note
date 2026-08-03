package wyoming

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxHeaderBytes  = 64 * 1024
	maxDataBytes    = 2 * 1024 * 1024
	maxPayloadBytes = 16 * 1024 * 1024
)

// Event is a single Wyoming protocol event. Data is JSON metadata and Payload
// is the optional binary body used by events such as audio-chunk.
type Event struct {
	Type    string
	Data    map[string]any
	Payload []byte
}

type eventHeader struct {
	Type          string         `json:"type"`
	Data          map[string]any `json:"data,omitempty"`
	DataLength    int            `json:"data_length,omitempty"`
	PayloadLength int            `json:"payload_length,omitempty"`
}

// WriteEvent writes the newline-delimited Wyoming event header followed by its
// optional JSON data and binary payload.
func WriteEvent(writer io.Writer, event Event) error {
	event.Type = strings.TrimSpace(event.Type)
	if event.Type == "" {
		return errors.New("Wyoming event type is required")
	}
	header := eventHeader{Type: event.Type}
	var data []byte
	var err error
	if len(event.Data) > 0 {
		data, err = json.Marshal(event.Data)
		if err != nil {
			return fmt.Errorf("encode Wyoming event data: %w", err)
		}
		if len(data) > maxDataBytes {
			return errors.New("Wyoming event data is too large")
		}
		header.DataLength = len(data)
	}
	if len(event.Payload) > maxPayloadBytes {
		return errors.New("Wyoming event payload is too large")
	}
	header.PayloadLength = len(event.Payload)
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("encode Wyoming event header: %w", err)
	}
	headerJSON = append(headerJSON, '\n')
	for _, part := range [][]byte{headerJSON, data, event.Payload} {
		if len(part) == 0 {
			continue
		}
		for len(part) > 0 {
			written, err := writer.Write(part)
			if err != nil {
				return fmt.Errorf("write Wyoming event: %w", err)
			}
			if written <= 0 {
				return io.ErrShortWrite
			}
			part = part[written:]
		}
	}
	return nil
}

// ReadEvent reads one size-bounded Wyoming event.
func ReadEvent(reader *bufio.Reader) (Event, error) {
	if reader == nil {
		return Event{}, errors.New("Wyoming event reader is required")
	}
	var headerJSON []byte
	for {
		fragment, continued, err := reader.ReadLine()
		if err != nil {
			return Event{}, fmt.Errorf("read Wyoming event header: %w", err)
		}
		if len(headerJSON)+len(fragment) > maxHeaderBytes {
			return Event{}, errors.New("Wyoming event header is too large")
		}
		headerJSON = append(headerJSON, fragment...)
		if !continued {
			break
		}
	}
	var header eventHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return Event{}, fmt.Errorf("decode Wyoming event header: %w", err)
	}
	if strings.TrimSpace(header.Type) == "" {
		return Event{}, errors.New("Wyoming event type is missing")
	}
	if header.DataLength < 0 || header.DataLength > maxDataBytes || header.PayloadLength < 0 || header.PayloadLength > maxPayloadBytes {
		return Event{}, errors.New("Wyoming event length is invalid")
	}
	data := header.Data
	if data == nil {
		data = make(map[string]any)
	}
	if header.DataLength > 0 {
		dataJSON := make([]byte, header.DataLength)
		if _, err := io.ReadFull(reader, dataJSON); err != nil {
			return Event{}, fmt.Errorf("read Wyoming event data: %w", err)
		}
		var trailing map[string]any
		if err := json.Unmarshal(dataJSON, &trailing); err != nil {
			return Event{}, fmt.Errorf("decode Wyoming event data: %w", err)
		}
		for key, value := range trailing {
			data[key] = value
		}
	}
	var payload []byte
	if header.PayloadLength > 0 {
		payload = make([]byte, header.PayloadLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return Event{}, fmt.Errorf("read Wyoming event payload: %w", err)
		}
	}
	return Event{Type: header.Type, Data: data, Payload: payload}, nil
}

type Endpoint struct {
	URL      string
	Hostname string
	Address  string
}

// ParseEndpoint accepts tcp://host:port as well as the convenient host:port
// form used by Wyoming integrations.
func ParseEndpoint(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, errors.New("Wyoming TCP endpoint is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "tcp://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "tcp" || parsed.User != nil || parsed.Hostname() == "" || parsed.Port() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Endpoint{}, errors.New("invalid Wyoming TCP endpoint")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return Endpoint{}, errors.New("invalid Wyoming TCP endpoint port")
	}
	address := net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port))
	return Endpoint{URL: "tcp://" + address, Hostname: parsed.Hostname(), Address: address}, nil
}
