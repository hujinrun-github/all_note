CREATE TRIGGER workspace_profile_verified_immutable
BEFORE UPDATE ON workspace_profile_versions
WHEN OLD.state = 'verified' AND (
  NEW.id <> OLD.id OR NEW.family_id <> OLD.family_id OR NEW.workspace_id <> OLD.workspace_id
  OR NEW.kind <> OLD.kind OR NEW.version <> OLD.version OR NEW.provider <> OLD.provider
  OR NEW.config_json <> OLD.config_json
  OR COALESCE(hex(NEW.secret_ciphertext),'') <> COALESCE(hex(OLD.secret_ciphertext),'')
  OR COALESCE(hex(NEW.secret_nonce),'') <> COALESCE(hex(OLD.secret_nonce),'')
  OR COALESCE(NEW.encryption_key_id,'') <> COALESCE(OLD.encryption_key_id,'')
)
BEGIN
  SELECT RAISE(ABORT, 'verified workspace profile version is immutable');
END;

CREATE TRIGGER system_profile_verified_immutable
BEFORE UPDATE ON system_profile_versions
WHEN OLD.state = 'verified' AND (
  NEW.id <> OLD.id OR NEW.family_id <> OLD.family_id OR NEW.kind <> OLD.kind
  OR NEW.version <> OLD.version OR NEW.provider <> OLD.provider OR NEW.config_json <> OLD.config_json
  OR COALESCE(hex(NEW.secret_ciphertext),'') <> COALESCE(hex(OLD.secret_ciphertext),'')
  OR COALESCE(hex(NEW.secret_nonce),'') <> COALESCE(hex(OLD.secret_nonce),'')
  OR COALESCE(NEW.encryption_key_id,'') <> COALESCE(OLD.encryption_key_id,'')
)
BEGIN
  SELECT RAISE(ABORT, 'verified system profile version is immutable');
END;
