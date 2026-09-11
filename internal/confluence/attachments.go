package confluence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// Attachments lists a page's attachments, up to max (0 = all).
func (c *Client) Attachments(ctx context.Context, pageID string, max int) ([]Content, error) {
	q := url.Values{"expand": {"version,container"}}
	return collect[Content](ctx, c, "/content/"+url.PathEscape(pageID)+"/child/attachment", q, max)
}

// Download streams an attachment into w. Only the site's own origin is
// contacted, whatever the download link says.
func (c *Client) Download(ctx context.Context, att Content, w io.Writer) (int64, error) {
	if att.Links.Download == "" {
		return 0, fmt.Errorf("attachment %s has no download link", att.ID)
	}
	u, err := c.resolveRef(c.WebURL(att.Links.Download))
	if err != nil {
		return 0, err
	}
	resp, err := c.do(ctx, request{method: http.MethodGet, url: u.String()})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, errorFrom(resp)
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("download %s: %w", att.Title, err)
	}
	return n, nil
}

// Upload holds attachment upload options.
type Upload struct {
	Comment   string
	MinorEdit bool
	Replace   bool // update an existing attachment with the same file name
}

// UploadAttachment attaches the file at path to a page. The file is streamed,
// and reopened if a rate-limited attempt has to be retried.
func (c *Client) UploadAttachment(ctx context.Context, pageID, path string, o Upload) (Content, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Content{}, fmt.Errorf("attachment file: %w", err)
	}
	if info.IsDir() {
		return Content{}, fmt.Errorf("attachment file %s is a directory", path)
	}
	boundary := multipart.NewWriter(io.Discard).Boundary()
	method := http.MethodPost
	if o.Replace {
		method = http.MethodPut
	}
	resp, err := c.do(ctx, request{
		method:      method,
		url:         c.apiURL(c.apiPath, "/content/"+url.PathEscape(pageID)+"/child/attachment", nil),
		contentType: "multipart/form-data; boundary=" + boundary,
		header:      map[string]string{"X-Atlassian-Token": "nocheck"}, // Data Center's XSRF check
		body:        func() (io.Reader, error) { return multipartBody(path, boundary, o) },
	})
	if err != nil {
		return Content{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Results []Content `json:"results"`
	}
	if err := decode(resp, &out); err != nil {
		return Content{}, err
	}
	if len(out.Results) == 0 {
		return Content{}, errors.New("upload succeeded but returned no attachment")
	}
	return out.Results[0], nil
}

// multipartBody streams the form through a pipe. The returned reader is an
// io.Closer, so the HTTP client closes it and the writer goroutine exits
// even when the request fails part-way.
func multipartBody(path, boundary string, o Upload) (io.Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("attachment file: %w", err)
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	if err := mw.SetBoundary(boundary); err != nil {
		f.Close()
		return nil, err
	}
	go func() {
		defer f.Close()
		pw.CloseWithError(writeParts(mw, f, filepath.Base(path), o))
	}()
	return pr, nil
}

func writeParts(mw *multipart.Writer, f io.Reader, name string, o Upload) error {
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if o.Comment != "" {
		if err := textField(mw, "comment", o.Comment); err != nil {
			return err
		}
	}
	if err := textField(mw, "minorEdit", strconv.FormatBool(o.MinorEdit)); err != nil {
		return err
	}
	return mw.Close()
}

// textField declares UTF-8 so non-ASCII comments are not misread.
func textField(mw *multipart.Writer, name, value string) error {
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, name))
	h.Set("Content-Type", "text/plain; charset=UTF-8")
	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, value)
	return err
}
