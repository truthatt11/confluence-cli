package convert

// UserKeys returns the distinct user keys mentioned in storage, in order of
// first appearance, so callers can resolve display names before converting.
func UserKeys(storage string) ([]string, error) {
	return distinctAttr(storage, "ri:user", "ri:userkey")
}

// ReferencedAttachments returns the distinct attachment filenames storage refers to.
func ReferencedAttachments(storage string) ([]string, error) {
	return distinctAttr(storage, "ri:attachment", "ri:filename")
}

// HasMacro reports whether storage contains the named macro.
func HasMacro(storage, name string) (bool, error) {
	root, err := parse(storage)
	if err != nil {
		return false, err
	}
	for _, m := range descendants(root, "ac:structured-macro") {
		if m.attrs["ac:name"] == name {
			return true, nil
		}
	}
	return false, nil
}

func distinctAttr(storage, element, attr string) ([]string, error) {
	root, err := parse(storage)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range descendants(root, element) {
		if v := n.attrs[attr]; v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
