package output

import (
	"encoding/json"
	"io"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func JSON(writer io.Writer, plan planning.Plan) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}
