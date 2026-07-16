ALTER TABLE voice_notes
  ADD COLUMN IF NOT EXISTS audio_revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS audio_state TEXT NOT NULL DEFAULT 'absent';

UPDATE voice_notes
SET audio_state = CASE upload_state
  WHEN 'uploading' THEN 'uploading'
  WHEN 'uploaded' THEN 'uploaded'
  ELSE 'absent'
END
WHERE audio_state = 'absent' AND upload_state IN ('uploading', 'uploaded');

ALTER TABLE voice_notes
  DROP CONSTRAINT IF EXISTS voice_notes_audio_state_check;

ALTER TABLE voice_notes
  ADD CONSTRAINT voice_notes_audio_state_check
  CHECK (audio_state IN ('absent', 'uploading', 'uploaded', 'delete_requested', 'deleted'));
