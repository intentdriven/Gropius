package archtest_test

import (
	"strings"
	"testing"
)

// An exempted setting is documented where an operator would look for it.
//
// The exemption table's fourth column names the surface a setting is reached
// through instead of the panel, and for both of today's entries that surface is
// config.json. A column saying so is a claim; this is what makes it a promise
// the build keeps. Without it, "reached through config.json, documented in
// docs/getting-started.md" could go on being true in the table long after the
// page stopped saying anything about the field — and the operator who cannot
// find the setting in the panel would then find it nowhere at all.
func TestExemptedSettingsAreDocumented(t *testing.T) {
	page := readDoc(t, "getting-started.md")
	for path, ex := range settingsPaneExemptions {
		if !strings.Contains(ex.ReachedThrough, "getting-started.md") {
			// An exemption reached through something else is not this test's
			// to hold, and it says so rather than passing in silence.
			t.Logf("%s is reached through %q, which this test does not check", path, ex.ReachedThrough)
			continue
		}
		if !strings.Contains(page, "`"+path+"`") {
			t.Errorf("docs/getting-started.md never names `%s`, which has no control in the panel and is "+
				"exempted on the grounds that the page says how to set it by hand", path)
		}
	}
}

// And the page says what it is for: that these are the settings with no
// control, and how to reach them. A page that mentioned the keys in passing
// would satisfy the check above and leave an operator no better off.
func TestTheDocumentationSaysWhichSettingsHaveNoControl(t *testing.T) {
	page := readDoc(t, "getting-started.md")
	for _, phrase := range []string{
		// That the panel is the normal way, and that these two are not on it.
		"Two are deliberately not there",
		// Where the file is, and the verb that reads it without opening it.
		"config.json",
		"gropius config show",
	} {
		if !containsAll(page, phrase) {
			t.Errorf("the getting-started page does not say %q, so the section the exemptions rest on is "+
				"not the section they were exempted for", phrase)
		}
	}
}

// Advertising is NOT documented as a hand-edited setting any more, because it
// has a control. A page still telling an operator to edit the file for it
// sends them to do by hand what the panel now does, on a setting where the
// hand edit is the one that loses to the next save.
func TestAdvertisingIsDocumentedAsAControl(t *testing.T) {
	if !containsAll(readDoc(t, "bind-address.md"), "Settings → Announce on the network") {
		t.Error("docs/bind-address.md does not name the control that switches advertising, so the page that " +
			"explains the advert leaves a reader looking for one")
	}
}
