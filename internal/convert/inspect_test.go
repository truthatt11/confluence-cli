package convert

import (
	"reflect"
	"testing"
)

const inspectSample = `<p><ac:link><ri:user ri:userkey="k1"/></ac:link> and <ac:link><ri:user ri:userkey="k2"/></ac:link>
<ac:link><ri:user ri:userkey="k1"/></ac:link></p>
<ac:image><ri:attachment ri:filename="a.png"/></ac:image>
<ac:structured-macro ac:name="view-file"><ac:parameter ac:name="name"><ri:attachment ri:filename="b.pdf"/></ac:parameter></ac:structured-macro>
<ac:link><ri:attachment ri:filename="a.png"/></ac:link>
<ac:structured-macro ac:name="children"/>`

func TestUserKeys(t *testing.T) {
	got, err := UserKeys(inspectSample)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"k1", "k2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReferencedAttachments(t *testing.T) {
	got, err := ReferencedAttachments(inspectSample)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.png", "b.pdf"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHasMacro(t *testing.T) {
	for name, want := range map[string]bool{"children": true, "view-file": true, "toc": false} {
		got, err := HasMacro(inspectSample, name)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("HasMacro(%q) = %v, want %v", name, got, want)
		}
	}
}
