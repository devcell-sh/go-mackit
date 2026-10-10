package qemu

import "fmt"

// qcodeFor maps printable ASCII to QEMU key codes. Shifted characters map
// to the unshifted key and are sent as a chord with "shift".
var qcodeFor = map[rune]string{
	' ': "spc", '-': "minus", '=': "equal", '[': "bracket_left", ']': "bracket_right",
	';': "semicolon", '\'': "apostrophe", '`': "grave_accent", '\\': "backslash",
	',': "comma", '.': "dot", '/': "slash", '\n': "ret", '\t': "tab",
}

var shiftedFor = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7', '*': '8', '(': '9', ')': '0',
	'_': '-', '+': '=', '{': '[', '}': ']', ':': ';', '"': '\'', '~': '`', '|': '\\',
	'<': ',', '>': '.', '?': '/',
}

// KeysForText converts text into a sequence of QMP send-key chords (one per
// character). Letters map to their key, uppercase and shifted symbols add
// "shift", newline is "ret" and tab is "tab". Non-ASCII runes are an error.
func KeysForText(text string) ([][]string, error) {
	chords := make([][]string, 0, len(text))
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			chords = append(chords, []string{string(r)})
		case r >= 'A' && r <= 'Z':
			chords = append(chords, []string{"shift", string(r - 'A' + 'a')})
		default:
			if base, ok := shiftedFor[r]; ok {
				code, isDigit := string(base), base >= '0' && base <= '9'
				if !isDigit {
					code = qcodeFor[base]
				}
				chords = append(chords, []string{"shift", code})
				continue
			}
			code, ok := qcodeFor[r]
			if !ok {
				return nil, fmt.Errorf("no QEMU key code for %q", r)
			}
			chords = append(chords, []string{code})
		}
	}
	return chords, nil
}
