package output

import (
	"fmt"
	"io"

	"github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

func UpdateHuman(writer io.Writer, plan update.Plan) error {
	_, err := fmt.Fprintf(
		writer,
		"Update %s\nComponent: %s\nCatalog: %s -> %s\nVersion: %s -> %s\nTarget plan: %s\nDigest: %s\n",
		plan.SchemaVersion,
		plan.ComponentID,
		plan.BeforeCatalogRevision,
		plan.AfterCatalogRevision,
		plan.Old.Version,
		plan.New.Version,
		plan.TargetPlan.Digest,
		plan.Digest,
	)
	return err
}

func UpdateResultHuman(writer io.Writer, result update.Result) error {
	if _, err := fmt.Fprintf(
		writer,
		"Update result %s\nDigest: %s\nCatalog: %s\n\n",
		result.SchemaVersion,
		result.UpdateDigest,
		result.CatalogRevision,
	); err != nil {
		return err
	}
	return ReceiptHuman(writer, result.Receipt)
}
