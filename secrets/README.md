# FlowSpace credential keyring

Docker Compose mounts `credentials-keyring.json` into the backend as a read-only
secret. The JSON file itself is ignored by Git and must never be committed.

Create a 32-byte key and write the keyring before the first start. PowerShell:

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$encoded = [Convert]::ToBase64String($bytes)
@{
  version = 1
  keys = @{ active = $encoded }
} | ConvertTo-Json -Depth 3 | Set-Content -Encoding utf8NoBOM .\secrets\credentials-keyring.json
```

Set `FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID=active`, then run `docker compose up`.
Back up this file through the deployment secret manager. Losing it makes saved
database, object-storage, AI, and Codex OAuth credentials unreadable.

For rotation, first deploy a keyring containing both the old and new key to all
instances. Change the active key only after every instance can read both keys.
Do not remove the old key until all ciphertext has been rewrapped and no control
row references the old key id.

