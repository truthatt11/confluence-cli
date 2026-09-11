package convert

import "testing"

func TestValidateStorage(t *testing.T) {
	valid := []string{
		`<p>Hello&nbsp;<strong>world</strong></p>`,
		`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[a < b]]></ac:plain-text-body></ac:structured-macro>`,
		`<p>line<br/>two</p><hr />`,
		``,
	}
	for _, s := range valid {
		if err := ValidateStorage(s); err != nil {
			t.Errorf("ValidateStorage(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{
		`<p>unclosed`,
		`<p>a<br>b</p>`,
		`<p><strong>crossed</p></strong>`,
		`<p>bad & ampersand</p>`,
		`<p>&unknownentity;</p>`,
	}
	for _, s := range invalid {
		if err := ValidateStorage(s); err == nil {
			t.Errorf("ValidateStorage(%q) = nil, want an error", s)
		}
	}
}
