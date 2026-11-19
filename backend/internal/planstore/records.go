package planstore

import (
	"encoding/json"
	"time"

	"github.com/hujinrun/flowspace/internal/webplanning"
)

// Private fields are deliberately excluded from the HTTP DTO. Keep them in a
// separate persisted envelope rather than accidentally dropping json:"-" data.
type draftRecord struct {
	Value                                                 webplanning.Draft
	WorkspaceID, OwnerID, RequestID, RequestHash          string
	AudioObjectKey, AudioMIME, AudioFilename, AudioDigest string
	AudioSize                                             int64
	AudioExpiresAt                                        *time.Time
	AudioPending                                          bool
	AudioCleanupAfter                                     time.Time
	CleanupObjectKeys                                     []string
}

func encodeDraft(d webplanning.Draft) (string, error) {
	b, err := json.Marshal(draftRecord{Value: d, WorkspaceID: d.WorkspaceID, OwnerID: d.OwnerID, RequestID: d.RequestID, RequestHash: d.RequestHash, AudioObjectKey: d.AudioObjectKey, AudioMIME: d.AudioMIME, AudioFilename: d.AudioFilename, AudioDigest: d.AudioDigest, AudioSize: d.AudioSize, AudioExpiresAt: d.AudioExpiresAt, AudioPending: d.AudioPending, CleanupObjectKeys: d.CleanupObjectKeys, AudioCleanupAfter: d.AudioCleanupAfter})
	return string(b), err
}
func decodeDraft(raw string) (webplanning.Draft, error) {
	var r draftRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return webplanning.Draft{}, err
	}
	d := r.Value
	d.WorkspaceID, d.OwnerID, d.RequestID, d.RequestHash = r.WorkspaceID, r.OwnerID, r.RequestID, r.RequestHash
	d.AudioObjectKey, d.AudioMIME, d.AudioFilename, d.AudioDigest = r.AudioObjectKey, r.AudioMIME, r.AudioFilename, r.AudioDigest
	d.AudioSize, d.AudioExpiresAt = r.AudioSize, r.AudioExpiresAt
	d.AudioPending, d.CleanupObjectKeys = r.AudioPending, r.CleanupObjectKeys
	d.AudioCleanupAfter = r.AudioCleanupAfter
	return d, nil
}

type jobRecord struct {
	Value                                                    webplanning.Job
	WorkspaceID, OwnerID, RequestID, RequestHash, LeaseToken string
	RetryOf                                                  string
	Revision, InputRevision                                  int64
	Attempt                                                  int
	LeaseUntil, CreatedAt, UpdatedAt                         time.Time
}

func encodeJob(j webplanning.Job) (string, error) {
	b, e := json.Marshal(jobRecord{Value: j, WorkspaceID: j.WorkspaceID, OwnerID: j.OwnerID, RequestID: j.RequestID, RequestHash: j.RequestHash, LeaseToken: j.LeaseToken, RetryOf: j.RetryOf, Revision: j.Revision, InputRevision: j.InputRevision, Attempt: j.Attempt, LeaseUntil: j.LeaseUntil, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt})
	return string(b), e
}
func decodeJob(raw string) (webplanning.Job, error) {
	var r jobRecord
	if e := json.Unmarshal([]byte(raw), &r); e != nil {
		return webplanning.Job{}, e
	}
	j := r.Value
	j.WorkspaceID, j.OwnerID, j.RequestID, j.RequestHash, j.LeaseToken = r.WorkspaceID, r.OwnerID, r.RequestID, r.RequestHash, r.LeaseToken
	j.RetryOf = r.RetryOf
	j.Revision, j.InputRevision, j.Attempt, j.LeaseUntil, j.CreatedAt, j.UpdatedAt = r.Revision, r.InputRevision, r.Attempt, r.LeaseUntil, r.CreatedAt, r.UpdatedAt
	return j, nil
}
