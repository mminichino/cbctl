package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// MaxDocumentBytes is the longest JSON Lines line the importer reads.
const MaxDocumentBytes = 10 << 20

// maxKeyIssues caps the key problems reported for one import entry.
const maxKeyIssues = 20

// DocumentKey returns the key a document gets from keyField: a non-empty string, or an integer
// formatted in base 10 ("-0" is "0"; 1000.0 and 1e3 are integers, 1.5 is not). The error
// says what is wrong with the value, without the document.
func DocumentKey(doc map[string]json.RawMessage, keyField string) (string, error) {
	raw, ok := doc[keyField]
	if !ok {
		return "", fmt.Errorf("%s is missing", keyField)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("%s is missing", keyField)
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("%s is not a valid string", keyField)
		}
		if s == "" {
			return "", fmt.Errorf("%s must not be an empty string", keyField)
		}
		return s, nil
	case 'n':
		return "", fmt.Errorf("%s must be a non-empty string or an integer (got null)", keyField)
	case 't', 'f':
		return "", fmt.Errorf("%s must be a non-empty string or an integer (got a boolean)", keyField)
	case '{':
		return "", fmt.Errorf("%s must be a non-empty string or an integer (got an object)", keyField)
	case '[':
		return "", fmt.Errorf("%s must be a non-empty string or an integer (got a list)", keyField)
	}
	r, ok := new(big.Rat).SetString(string(raw))
	if !ok || !r.IsInt() {
		return "", fmt.Errorf("%s must be a non-empty string or an integer (got %s)", keyField, string(raw))
	}
	if r.Num().BitLen() > 63 {
		return "", fmt.Errorf("%s is an integer too large to be a key (got %s)", keyField, string(raw))
	}
	return r.Num().String(), nil
}

// objectFields decodes raw as a JSON object keeping the values undecoded.
func objectFields(raw []byte) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("not valid JSON")
	}
	return m, nil
}

// checkInlineKeys checks the keyField of every inline document: present, a usable type and
// unique. It records at most maxKeyIssues problems.
func (c *collector) checkInlineKeys(pointer string, data []json.RawMessage, keyField string) {
	seen := map[string]int{}
	reported := 0
	for i, raw := range data {
		fields, err := objectFields(raw)
		if err != nil {
			continue // reported by checkInlineData
		}
		key, err := DocumentKey(fields, keyField)
		switch {
		case err != nil:
			reported++
			if reported <= maxKeyIssues {
				c.add(ptr(pointer, i), "keyField "+err.Error())
			}
		case seen[key] != 0:
			reported++
			if reported <= maxKeyIssues {
				c.addf(ptr(pointer, i), "keyField %s: key %s duplicates data[%d]", keyField, quoteKey(key), seen[key]-1)
			}
		default:
			seen[key] = i + 1
		}
	}
	if reported > maxKeyIssues {
		c.addf(pointer, "keyField %s: %d more problems not shown", keyField, reported-maxKeyIssues)
	}
}

func quoteKey(k string) string {
	if len(k) > 40 {
		k = k[:40] + "…"
	}
	return `"` + strings.ReplaceAll(k, `"`, `\"`) + `"`
}

// checkInlineData checks that data is not empty and every item is a JSON object. Timestamps,
// anchors and aliases were already converted by the YAML reader; !!binary values and non-string
// keys fail the whole document there.
func (c *collector) checkInlineData(pointer string, data []json.RawMessage) {
	if len(data) == 0 {
		c.add(pointer, "data must not be empty", "list the documents to import, or remove the import entry")
		return
	}
	for i, raw := range data {
		if _, err := objectFields(raw); err != nil {
			c.add(ptr(pointer, i), "each data item must be a mapping that becomes one document")
		}
	}
}
