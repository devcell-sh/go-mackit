package media

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func decodePlist(r io.Reader) (any, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	for {
		t, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("reading plist header: %w", err)
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "plist" {
			v, err := decodePlistValue(d)
			if err != nil {
				return nil, fmt.Errorf("decoding plist body: %w", err)
			}
			return v, nil
		}
	}
}

func decodePlistValue(d *xml.Decoder) (any, error) {
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tok := t.(type) {
		case xml.StartElement:
			switch tok.Name.Local {
			case "dict":
				return decodePlistDict(d)
			case "array":
				return decodePlistArray(d)
			case "string":
				return decodePlistText(d)
			case "integer":
				s, err := decodePlistText(d)
				if err != nil {
					return nil, err
				}
				return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			case "real":
				s, err := decodePlistText(d)
				if err != nil {
					return nil, err
				}
				return strconv.ParseFloat(strings.TrimSpace(s), 64)
			case "true":
				d.Skip()
				return true, nil
			case "false":
				d.Skip()
				return false, nil
			case "data", "date":
				return decodePlistText(d)
			default:
				d.Skip()
			}
		case xml.EndElement:
			return nil, nil
		case xml.CharData:
			continue
		}
	}
}

func decodePlistDict(d *xml.Decoder) (map[string]any, error) {
	m := make(map[string]any)
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tok := t.(type) {
		case xml.StartElement:
			if tok.Name.Local != "key" {
				return nil, fmt.Errorf("expected <key>, got <%s>", tok.Name.Local)
			}
			key, err := decodePlistText(d)
			if err != nil {
				return nil, err
			}
			val, err := decodePlistValue(d)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			m[key] = val
		case xml.EndElement:
			return m, nil
		case xml.CharData:
			continue
		}
	}
}

func decodePlistArray(d *xml.Decoder) ([]any, error) {
	var arr []any
	for {
		val, err := decodePlistValue(d)
		if err != nil {
			return nil, err
		}
		if val == nil {
			return arr, nil
		}
		arr = append(arr, val)
	}
}

func decodePlistText(d *xml.Decoder) (string, error) {
	var sb strings.Builder
	for {
		t, err := d.Token()
		if err != nil {
			return "", err
		}
		switch tok := t.(type) {
		case xml.CharData:
			sb.Write(tok)
		case xml.EndElement:
			return sb.String(), nil
		}
	}
}

func plistString(v any) string {
	s, _ := v.(string)
	return s
}

func plistDict(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func plistArray(v any) []any {
	a, _ := v.([]any)
	return a
}

func plistInt(v any) int64 {
	n, _ := v.(int64)
	return n
}
