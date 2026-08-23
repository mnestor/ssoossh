package config

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
)

// parsePolicyPlist decodes a flat plist <dict> into a Go map, recognizing
// only the scalar value types the platform-native policy settings use:
// <string>, <integer>, <true/>, and <false/>. A key whose value is any
// other plist type (<array>, <dict>, <date>, <data>, <real>) is skipped
// rather than rejected — this parser only needs to extract the handful of
// settings ssoossh understands, not to be a general-purpose plist decoder.
// A document that isn't well-formed XML, or has no root <dict>, is an
// error.
func parsePolicyPlist(data []byte) (map[string]any, error) {
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
