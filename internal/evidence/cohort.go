package evidence

import (
	"errors"
	"regexp"
	"time"
)

var certificationCohortPattern = regexp.MustCompile(
	`^cert-([0-9]{8}T[0-9]{6}Z)-([0-9a-f]{8})$`,
)

func ValidateCertificationCohort(value string) error {
	match := certificationCohortPattern.FindStringSubmatch(value)
	if match == nil {
		return errors.New(
			"certification cohort must match cert-YYYYMMDDThhmmssZ-<commit8>",
		)
	}
	if _, err := time.Parse("20060102T150405Z", match[1]); err != nil {
		return errors.New("certification cohort contains an invalid UTC timestamp")
	}
	return nil
}

func CertificationCohortCommitPrefix(value string) (string, error) {
	if err := ValidateCertificationCohort(value); err != nil {
		return "", err
	}
	return certificationCohortPattern.FindStringSubmatch(value)[2], nil
}

func CertificationCohortTimestamp(value string) (time.Time, error) {
	if err := ValidateCertificationCohort(value); err != nil {
		return time.Time{}, err
	}
	return time.Parse(
		"20060102T150405Z",
		certificationCohortPattern.FindStringSubmatch(value)[1],
	)
}
