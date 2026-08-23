package config

import "fmt"

// SourceStatus is what happened when a configuration source was consulted.
type SourceStatus string

const (
	// SourceMerged means the source contributed values.
	SourceMerged SourceStatus = "merged"
	// SourceAbsent means the file was not there, which is normal for every
	// location except one the caller named explicitly.
	SourceAbsent SourceStatus = "absent"
	// SourceError means the source existed but could not be used —
	// unreadable, or not parseable as YAML. Loading continues (that is the
	// long-standing behavior) but the values are silently missing, which is
	// exactly the failure this status exists to make visible.
	SourceError SourceStatus = "error"
	// SourceNotGiven means an optional source was not selected at all, such
	// as --config or `enforce`.
	SourceNotGiven SourceStatus = "not given"
)

// ConfigSource records one entry in the configuration merge chain, in the
// order it was applied. Recorded on every load rather than only under
// --debug: the cost is a few strings, and the alternative is asking a user
// to reproduce a problem with a different command than the one that failed.
//
// This exists because mergeConfig deliberately ignores errors — a missing
// file at any search location is normal — which also means a config file
// with a YAML syntax error is skipped without a word. From the outside that
// is indistinguishable from the file not being read at all.
type ConfigSource struct {
	// Label names the source for a human: "user file", "enforce", and so on.
	Label string
	// Path is the file consulted, empty for sources that are not files.
	Path string
	// Status is what came of it.
	Status SourceStatus
	// Err is the failure text when Status is SourceError.
	Err string
	// Detail is a short human note about what this source contributed —
	// which flags were set, for instance. Empty for most sources.
	Detail string
	// AdminLock marks a source an ordinary user cannot override: the
	// `enforce` file and platform-native policy. Reported separately
	// because "last one wins" describes the mechanism but not the intent,
	// and a user reading the chain needs to know which entries they cannot
	// argue with.
	AdminLock bool
}

// describeFailure says why a source did not contribute, for an error
// message about a file the user named explicitly. Only meaningful when
// Status is not SourceMerged.
func (s ConfigSource) describeFailure() string {
	if s.Status == SourceError {
		return s.Err
	}
	return "no such file"
}

// String renders one line of the merge chain.
func (s ConfigSource) String() string {
	target := s.Label
	if s.Path != "" {
		target = fmt.Sprintf("%s (%s)", s.Label, s.Path)
	}
	if s.Status == SourceError {
		return fmt.Sprintf("%s: %s: %s", target, s.Status, s.Err)
	}
	return fmt.Sprintf("%s: %s", target, s.Status)
}
