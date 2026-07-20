CREATE OR REPLACE FUNCTION reject_verified_profile_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.state = 'verified' AND (
    NEW.id IS DISTINCT FROM OLD.id OR NEW.family_id IS DISTINCT FROM OLD.family_id
    OR NEW.kind IS DISTINCT FROM OLD.kind OR NEW.version IS DISTINCT FROM OLD.version
    OR NEW.provider IS DISTINCT FROM OLD.provider OR NEW.config_json IS DISTINCT FROM OLD.config_json
    OR NEW.secret_ciphertext IS DISTINCT FROM OLD.secret_ciphertext
    OR NEW.secret_nonce IS DISTINCT FROM OLD.secret_nonce
    OR NEW.encryption_key_id IS DISTINCT FROM OLD.encryption_key_id
  ) THEN
    RAISE EXCEPTION 'verified profile version is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER workspace_profile_verified_immutable
BEFORE UPDATE ON workspace_profile_versions
FOR EACH ROW EXECUTE FUNCTION reject_verified_profile_mutation();

CREATE TRIGGER system_profile_verified_immutable
BEFORE UPDATE ON system_profile_versions
FOR EACH ROW EXECUTE FUNCTION reject_verified_profile_mutation();
