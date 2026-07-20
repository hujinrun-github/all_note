package tenantmigration

import (
	"errors"
	"fmt"
)

func Verify(expected, actual TransferManifest) error {
	if expected.WorkspaceID != actual.WorkspaceID {
		return errors.New("workspace identity mismatch")
	}
	if expected.Schema != actual.Schema {
		return errors.New("tenant schema capability mismatch")
	}
	for capability, required := range expected.Capabilities {
		if required && !actual.Capabilities[capability] {
			return errors.New("tenant schema capability mismatch")
		}
	}
	if len(expected.Tables) != len(actual.Tables) {
		return errors.New("logical table set mismatch")
	}
	for index, want := range expected.Tables {
		got := actual.Tables[index]
		if want.Name != got.Name {
			return errors.New("logical table set mismatch")
		}
		if want.Rows != got.Rows {
			return fmt.Errorf("logical table %s row count mismatch", want.Name)
		}
		if want.PrimaryKeyHash != got.PrimaryKeyHash {
			return fmt.Errorf("logical table %s primary key mismatch", want.Name)
		}
		if want.CriticalHash != got.CriticalHash {
			return fmt.Errorf("logical table %s content hash mismatch", want.Name)
		}
		if want.MaxRevision != got.MaxRevision {
			return fmt.Errorf("logical table %s revision mismatch", want.Name)
		}
	}
	if expected.LogicalHash != actual.LogicalHash {
		return errors.New("logical manifest hash mismatch")
	}
	return nil
}
