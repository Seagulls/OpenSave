//go:build windows

package main

import "testing"

func TestFileURIForAToastPicture(t *testing.T) {
	if got := fileURI(`C:\Users\Example User\AppData\Local\Temp\OpenSave\notifications\ab.jpg`); got != "file:///C:/Users/Example%20User/AppData/Local/Temp/OpenSave/notifications/ab.jpg" {
		t.Errorf("fileURI = %q", got)
	}
}
