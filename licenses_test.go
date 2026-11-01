package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Every module compiled into the binary must have its notice in the binary.
func TestLicensesCoverEveryModule(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}", ".").Output()
	if err != nil {
		t.Skip("go list is not available: ", err)
	}
	for _, module := range strings.Fields(string(out)) {
		if !strings.Contains(thirdPartyLicenses, "\n== "+module+" ==\n") {
			t.Errorf("%s is missing: run go generate", module)
		}
	}
	for _, want := range []string{"\n== Go standard library ==\n", "\n== devkit-utils ==\n", "Valve Software"} {
		if !strings.Contains(thirdPartyLicenses, want) {
			t.Errorf("%q is missing", want)
		}
	}
}

func TestLicensesCommand(t *testing.T) {
	if err := run([]string{"licenses"}); err != nil {
		t.Error(err)
	}
}
