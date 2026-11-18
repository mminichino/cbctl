package manifest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// checkImportFile checks that an import file can be read. With a keyField it also reads the
// whole file and checks every document's key: present, usable and unique. Problems read
// "data/schedules.jsonl:812: flightId duplicates line 17".
func (c *collector) checkImportFile(ctx context.Context, pointer, shown, abs, keyField string) {
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		c.add(ptr(pointer, "file"), fmt.Sprintf("cannot read %s: %s", shown, errText(err)),
			"relative paths resolve from the manifest's directory (from the working directory with -f -)")
		return
	case st.IsDir():
		c.add(ptr(pointer, "file"), shown+" is a directory, not a JSON Lines file")
		return
	}
	if keyField == "" {
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		c.add(ptr(pointer, "file"), fmt.Sprintf("cannot read %s: %s", shown, errText(err)))
		return
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	seen := map[string]int{}
	reported := 0
	report := func(format string, a ...any) {
		reported++
		if reported <= maxKeyIssues {
			c.add(ptr(pointer, "file"), fmt.Sprintf(format, a...))
		}
	}
	for line := 1; ; line++ {
		if ctx.Err() != nil {
			return
		}
		text, tooLong, readErr := readLine(br)
		if len(bytes.TrimSpace(text)) > 0 || tooLong {
			c.fileLine(report, seen, shown, line, text, tooLong, keyField)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				report("%s: read failed: %s", shown, errText(readErr))
			}
			break
		}
	}
	if reported > maxKeyIssues {
		c.add(ptr(pointer, "file"), fmt.Sprintf("%s: %d more problems not shown", shown, reported-maxKeyIssues))
	}
}

func (c *collector) fileLine(report func(string, ...any), seen map[string]int, shown string, line int, text []byte, tooLong bool, keyField string) {
	if tooLong {
		report("%s:%d: the line is longer than %d MiB", shown, line, MaxDocumentBytes>>20)
		return
	}
	fields, err := objectFields(text)
	if err != nil {
		report("%s:%d: %s", shown, line, err)
		return
	}
	key, err := DocumentKey(fields, keyField)
	switch {
	case err != nil:
		report("%s:%d: %s", shown, line, err)
	case seen[key] != 0:
		report("%s:%d: %s duplicates line %d (key %s)", shown, line, keyField, seen[key], quoteKey(key))
	default:
		seen[key] = line
	}
}

// readLine reads one line without its newline. A line longer than MaxDocumentBytes is skipped
// and reported through tooLong. The error is io.EOF after the last line.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		part, isPrefix, rerr := br.ReadLine()
		if rerr != nil {
			return buf, tooLong, rerr
		}
		if !tooLong {
			buf = append(buf, part...)
			if len(buf) > MaxDocumentBytes {
				tooLong, buf = true, nil
			}
		}
		if !isPrefix {
			return buf, tooLong, nil
		}
	}
}

// errText is err without the path the OS error repeats.
func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
