package utils

import "testing"

func TestFileURL(t *testing.T) {
	for path, want := range map[string]string{
		`C:\repo\report.html`:                "file:///C:/repo/report.html",
		`C:\project space\report.html`:       "file:///C:/project%20space/report.html",
		`C:\Temp\webdriverio_observer.mjs`:   "file:///C:/Temp/webdriverio_observer.mjs",
		`\\server\share\report.html`:         "file://server/share/report.html",
		`\\server\share space\report#1.html`: "file://server/share%20space/report%231.html",
		`/repo space/report#1.html`:          "file:///repo%20space/report%231.html",
		`/repo\name/report.html`:             "file:///repo%5Cname/report.html",
	} {
		if got := FileURL(path); got != want {
			t.Errorf("FileURL(%q) = %q, want %q", path, got, want)
		}
	}
}
