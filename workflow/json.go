package workflow

import (
	"github.com/go-json-experiment/json"
)

// init turns the `format` struct tag option back on for every call into github.com/go-json-experiment/json.
// Durations in this package and in the storage schemas are tagged `format:iso8601`, and that is the form plans are
// stored in. Newer versions of the json package ignore `format` unless asked (https://go.dev/issue/79071) and fail to
// marshal the fields instead. Every package that encodes these types imports this one, so this runs before any of
// them encodes. Remove this, and the tags, once the json package supports typed struct tags.
func init() {
	json.ExperimentalGlobalSupportFormatTag(true)
}
