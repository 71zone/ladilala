package data

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Encoding names accepted by CSVOptions.Encoding and reported by Sniff.
const (
	EncUTF8        = "utf-8"
	EncUTF8BOM     = "utf-8-bom"
	EncUTF16LE     = "utf-16le"
	EncUTF16BE     = "utf-16be"
	EncWindows1258 = "windows-1258"
	EncWindows1252 = "windows-1252"
)

const sniffSize = 64 * 1024

// detectEncoding looks at the first bytes of a file.
func detectEncoding(sample []byte, full bool) string {
	switch {
	case bytes.HasPrefix(sample, []byte{0xEF, 0xBB, 0xBF}):
		return EncUTF8BOM
	case bytes.HasPrefix(sample, []byte{0xFF, 0xFE}):
		return EncUTF16LE
	case bytes.HasPrefix(sample, []byte{0xFE, 0xFF}):
		return EncUTF16BE
	}
	if enc := guessUTF16NoBOM(sample); enc != "" {
		return enc
	}
	if validUTF8Sample(sample, full) {
		return EncUTF8
	}
	if looksWindows1258(sample) {
		return EncWindows1258
	}
	return EncWindows1252
}

// guessUTF16NoBOM finds UTF-16 text without a BOM: ASCII text has a zero byte
// in every second position.
func guessUTF16NoBOM(s []byte) string {
	if len(s) < 4 {
		return ""
	}
	var even, odd int
	for i, b := range s {
		if b == 0 {
			if i%2 == 0 {
				even++
			} else {
				odd++
			}
		}
	}
	pairs := len(s) / 2
	switch {
	case odd*10 > pairs*3 && even == 0:
		return EncUTF16LE
	case even*10 > pairs*3 && odd == 0:
		return EncUTF16BE
	}
	return ""
}

// validUTF8Sample tolerates a multi-byte character cut at the end of a sample.
func validUTF8Sample(s []byte, full bool) bool {
	if utf8.Valid(s) {
		return true
	}
	if !full {
		return false
	}
	for cut := 1; cut <= 3 && cut < len(s); cut++ {
		if utf8.Valid(s[:len(s)-cut]) {
			return true
		}
	}
	return false
}

// looksWindows1258 tells Windows-1258 from Windows-1252. Windows-1258 has the
// Vietnamese letters (Ă Â Đ Ê Ô Ơ Ư) and the combining tone marks. The tone
// marks follow a Latin letter. Western text in 1252 rarely has these bytes.
func looksWindows1258(s []byte) bool {
	letters := map[byte]bool{0xC3: true, 0xE3: true, 0xD0: true, 0xF0: true, 0xD5: true, 0xF5: true, 0xDD: true, 0xFD: true}
	tones := map[byte]bool{0xCC: true, 0xEC: true, 0xDE: true, 0xD2: true, 0xF2: true}
	score := 0
	for i, b := range s {
		if letters[b] && (b == 0xD0 || b == 0xF0 || b == 0xDD || b == 0xFD || b == 0xD5 || b == 0xF5) {
			score++
		}
		if tones[b] && i > 0 && isASCIILetter(s[i-1]) {
			score += 2
		}
	}
	return score > 0
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// decodeReader wraps r so that it yields UTF-8 in NFC form.
func decodeReader(r io.Reader, enc string) (io.Reader, error) {
	var t transform.Transformer
	switch enc {
	case EncUTF8:
	case EncUTF8BOM:
		br := bufio.NewReader(r)
		if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
			_, _ = br.Discard(3)
		}
		r = br
	case EncUTF16LE:
		t = unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
	case EncUTF16BE:
		t = unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder()
	case EncWindows1258:
		t = charmap.Windows1258.NewDecoder()
	case EncWindows1252:
		t = charmap.Windows1252.NewDecoder()
	default:
		return nil, fmt.Errorf("unknown encoding %q", enc)
	}
	if t == nil {
		return transform.NewReader(r, norm.NFC), nil
	}
	return transform.NewReader(r, transform.Chain(t, norm.NFC)), nil
}

// sniffDelimiter picks the delimiter that gives the most consistent columns.
func sniffDelimiter(text []byte) rune {
	best, bestScore := ',', 0
	for _, d := range []rune{',', ';', '\t'} {
		r := csv.NewReader(bytes.NewReader(text))
		r.Comma = d
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		want, score := -1, 0
		for range 50 {
			rec, err := r.Read()
			if err != nil {
				break
			}
			if want < 0 {
				want = len(rec)
				if want < 2 {
					break
				}
				score = 1
				continue
			}
			if len(rec) == want {
				score += 2
			}
		}
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

// Sniff reports the encoding and the delimiter of a CSV file.
func Sniff(path string) (encoding string, delimiter rune, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()
	buf := make([]byte, sniffSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", 0, fmt.Errorf("cannot read file: %w", err)
	}
	if n == 0 {
		return "", 0, ErrEmpty
	}
	buf = buf[:n]
	encoding = detectEncoding(buf, n == sniffSize)
	dec, err := decodeReader(bytes.NewReader(buf), encoding)
	if err != nil {
		return "", 0, err
	}
	text, _ := io.ReadAll(dec)
	// Drop a cut last line so a half row does not skew the score.
	if i := bytes.LastIndexByte(text, '\n'); i > 0 && n == sniffSize {
		text = text[:i]
	}
	return encoding, sniffDelimiter(text), nil
}
