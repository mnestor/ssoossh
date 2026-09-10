package config

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"

	"howett.net/plist"
)

// parsePolicyPlist decodes a flat plist <dict> into a Go map, recognizing
// only the value types the platform-native policy settings use: the scalars
// <string>, <integer>, <true/> and <false/>, plus an <array> of <string>
// for the one list-valued setting (forbidden_certificate_extensions). A key
// whose value is any other plist type (<dict>, <date>, <data>, <real>) is
// skipped rather than rejected — this parser only needs to extract the
// handful of settings ssoossh understands, not to be a general-purpose
// plist decoder.
//
// Skipping <dict> is why sshkey.type and sshkey.size are flat keys with a
// literal dot rather than a nested dictionary: a nested one would be
// dropped in silence. See buildPolicyMap, which does the un-flattening.
//
// A document that isn't well-formed XML, or has no root <dict>, is an
// error.
func parsePolicyPlist(data []byte) (map[string]any, error) {
	// macOS does not keep managed preferences as the XML an administrator
	// uploaded: it rewrites every file under /Library/Managed Preferences
	// as an Apple binary property list, Apple's own MCX and loginwindow
	// files included. An XML-only parser therefore failed on every real
	// managed Mac, reporting the bplist00 magic as "XML syntax error ...
	// invalid UTF-8" and taking the whole config load down with it -- so
	// device-scoped policy had never once loaded on managed hardware.
	//
	// Sniffed and branched rather than routed wholesale through the binary
	// library: the XML path below is fuzzed and its skip-what-we-do-not-
	// understand semantics are pinned by tests, and a hand-written .plist
	// or a .mobileconfig payload is still XML. Only files macOS has
	// rewritten take the new path.
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return parseBinaryPolicyPlist(data)
	}

	dec := xml.NewDecoder(bytes.NewReader(data))

	if err := seekRootDict(dec); err != nil {
		return nil, err
	}

	values := map[string]any{}
	for {
		key, value, ok, done, err := readDictEntry(dec)
		if err != nil {
			return nil, err
		}
		if done {
			return values, nil
		}
		if ok {
			values[key] = value
		}
	}
}

// readDictEntry reads one <key>/value pair from the current position
// inside a plist <dict>, skipping any character data (e.g. whitespace)
// between entries. done reports the root <dict>'s closing tag. ok is false
// when the value's type isn't one this parser understands — the entry is
// still consumed correctly, just not returned.
func readDictEntry(dec *xml.Decoder) (key string, value any, ok bool, done bool, err error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", nil, false, false, fmt.Errorf("read plist: %w", err)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			return "", nil, false, true, nil

		case xml.StartElement:
			if t.Name.Local != "key" {
				return "", nil, false, false, fmt.Errorf("expected <key>, found <%s>", t.Name.Local)
			}
			return readKeyedValue(dec)
		}
	}
}

// readKeyedValue reads a <key> element's text, then the value element that
// follows it.
func readKeyedValue(dec *xml.Decoder) (key string, value any, ok bool, done bool, err error) {
	key, err = readCharData(dec)
	if err != nil {
		return "", nil, false, false, fmt.Errorf("read plist key: %w", err)
	}

	valueTok, err := nextStartElement(dec)
	if err != nil {
		return "", nil, false, false, fmt.Errorf("read value for plist key %q: %w", key, err)
	}
	value, ok, err = readScalarValue(dec, valueTok)
	if err != nil {
		return "", nil, false, false, fmt.Errorf("read value for plist key %q: %w", key, err)
	}
	return key, value, ok, false, nil
}

// seekRootDict advances dec past the plist header (XML declaration,
// DOCTYPE, the wrapping <plist> element) to the start of the document's
// root <dict>, leaving the decoder positioned to read that dict's children
// next.
func seekRootDict(dec *xml.Decoder) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("no root <dict> found: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "dict" {
			return nil
		}
	}
}

// nextStartElement returns the next StartElement token, skipping any
// intervening character data (e.g. whitespace between tags).
func nextStartElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se, nil
		}
	}
}

// readCharData reads character data up to the next end element, for
// <key> and <string> contents.
func readCharData(dec *xml.Decoder) (string, error) {
	var buf bytes.Buffer
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			buf.Write(t)
		case xml.EndElement:
			return buf.String(), nil
		}
	}
}

// readScalarValue interprets start as a plist value element. It returns
// ok=false (having still consumed the whole subtree, so the caller's
// position in the document stays correct) for any type this parser
// doesn't need to understand. Arrays of strings are handled as a special
// case — they become []any with string elements.
func readScalarValue(dec *xml.Decoder, start xml.StartElement) (value any, ok bool, err error) {
	switch start.Name.Local {
	case "string":
		s, err := readCharData(dec)
		return s, true, err

	case "integer":
		s, err := readCharData(dec)
		if err != nil {
			return nil, false, err
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid <integer>%s</integer>: %w", s, err)
		}
		return n, true, nil

	case "true":
		return true, true, skipToEnd(dec)

	case "false":
		return false, true, skipToEnd(dec)

	case "array":
		arr, err := readArrayOfStrings(dec)
		return arr, err == nil, err

	default:
		return nil, false, skipToEnd(dec)
	}
}

// readArrayOfStrings reads an <array> of <string> elements, returning an
// []any with the string contents. Non-string elements are skipped without error.
// This is used for policy list values like forbidden certificate extensions.
func readArrayOfStrings(dec *xml.Decoder) ([]any, error) {
	var result []any
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("read array: %w", err)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			return result, nil
		case xml.StartElement:
			if t.Name.Local == "string" {
				s, err := readCharData(dec)
				if err != nil {
					return nil, fmt.Errorf("read array string: %w", err)
				}
				result = append(result, s)
			} else {
				// Skip any non-string element
				if err := skipToEnd(dec); err != nil {
					return nil, fmt.Errorf("skip non-string array element: %w", err)
				}
			}
		}
	}
}

// skipToEnd discards tokens through the EndElement matching the
// StartElement the caller already consumed, so an unsupported value's
// subtree (a nested <dict>/<array>, or a self-closing element like
// <true/>) doesn't confuse the caller's position in the document.
func skipToEnd(dec *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// parseBinaryPolicyPlist decodes an Apple binary property list into the
// same shape the XML path produces: a flat map of the value types the
// policy settings use.
//
// The format is not hand-rolled here. It has an offset table, variable
// width object references and its own string encodings, and a decoder for
// it is a great deal of offset arithmetic to get wrong in a path that
// decides security settings. howett.net/plist is pure Go, so it costs
// nothing against the CGO_ENABLED=0 cross-compile the macOS client is
// built with -- which is the same constraint that rules out reading these
// values through CFPreferences, the API Apple actually intends for this.
func parseBinaryPolicyPlist(data []byte) (map[string]any, error) {
	var root map[string]any
	if _, err := plist.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode binary plist: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("no root <dict> found: binary plist has no top-level dictionary")
	}

	// Filtered to the same accepted types as the XML path, so which
	// on-disk format macOS happened to write cannot change which settings
	// apply. Anything else -- a nested dict, a date, a real, raw data --
	// is skipped rather than rejected, exactly as it is there.
	out := make(map[string]any, len(root))
	for key, value := range root {
		if v, ok := acceptPolicyValue(value); ok {
			out[key] = v
		}
	}
	return out, nil
}

// acceptPolicyValue narrows one decoded binary-plist value to the types a
// policy setting may take, mirroring readScalarValue's choices on the XML
// side. Integers arrive from the decoder in whichever width fits, and are
// normalised to int64 so a setting reads the same either way.
func acceptPolicyValue(value any) (any, bool) {
	switch v := value.(type) {
	case string, bool:
		return v, true
	case int64:
		return v, true
	case uint64:
		// Out of int64 range is not a setting anyone wrote: the integers
		// here are key sizes. Skipped rather than wrapped, since a
		// silently negative sshkey.size is worse than an absent one.
		if v > math.MaxInt64 {
			return nil, false
		}
		return int64(v), true
	case []any:
		// An array of strings, as the XML path builds for
		// forbidden_certificate_extensions. Non-string elements are
		// dropped rather than failing the whole file, matching
		// readArrayOfStrings.
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}
