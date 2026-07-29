package output

import (
	"fmt"
	"io"

	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

func ReceiptHuman(writer io.Writer, receipt state.Receipt) error {
	if _, err := fmt.Fprintf(
		writer,
		"Apply %s\nTarget: %s\nComplete: %t\n\n",
		receipt.PlanDigest,
		receipt.TargetID,
		receipt.Complete,
	); err != nil {
		return err
	}
	for _, outcome := range receipt.Outcomes {
		if _, err := fmt.Fprintf(
			writer,
			"- %-16s %-36s requested=%s installed=%s verified=%s",
			outcome.Status,
			outcome.ActionID,
			outcome.RequestedVersion,
			outcome.InstalledVersion,
			outcome.VerifiedVersion,
		); err != nil {
			return err
		}
		if outcome.Noop {
			if _, err := fmt.Fprint(writer, " noop=true"); err != nil {
				return err
			}
		}
		if outcome.Reason != "" {
			if _, err := fmt.Fprint(writer, " reason="+outcome.Reason); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	return nil
}
