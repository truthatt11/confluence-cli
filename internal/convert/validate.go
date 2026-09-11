package convert

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ValidateStorage checks that s is well-formed storage format (XHTML with
// HTML entities), which Confluence requires before it accepts a write. It
// catches the usual hand-editing mistakes, such as an unclosed <br> or a
// bare '&', before they reach the server.
func ValidateStorage(s string) error {
	d := xml.NewDecoder(strings.NewReader("<" + rootName + ">" + s + "</" + rootName + ">"))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var syntax *xml.SyntaxError
			if errors.As(err, &syntax) {
				return fmt.Errorf("invalid storage format on line %d: %s", syntax.Line, syntax.Msg)
			}
			return fmt.Errorf("invalid storage format: %w", err)
		}
	}
}
