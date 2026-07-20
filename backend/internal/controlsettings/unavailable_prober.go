package controlsettings

import (
	"context"
	"errors"
)

type UnavailableProber struct{}

func (UnavailableProber) Probe(context.Context, string, string, []byte, []byte) (ProbeResult, error) {
	return ProbeResult{}, errors.New("service profile probing is not configured")
}
