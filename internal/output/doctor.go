package output

import (
	"fmt"
	"io"

	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
)

func DoctorHuman(writer io.Writer, report doctor.Report) error {
	if _, err := fmt.Fprintf(
		writer,
		"Doctor %s\nTarget: %s\nReady: %t\n\n",
		report.SchemaVersion,
		report.Target.ID.String(),
		report.Ready,
	); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(
			writer,
			"- %-16s %-24s requested=%s installed=%s code=%s",
			check.Status,
			check.ComponentID,
			check.RequestedVersion,
			check.InstalledVersion,
			check.ReasonCode,
		); err != nil {
			return err
		}
		if check.Reason != "" {
			if _, err := fmt.Fprint(writer, " reason="+check.Reason); err != nil {
				return err
			}
		}
		if check.RecoveryHint != "" {
			if _, err := fmt.Fprint(writer, " hint="+check.RecoveryHint); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	return nil
}
