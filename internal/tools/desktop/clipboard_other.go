//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

func clipboardRead() (string, error) {
	if runtime.GOOS == "darwin" {
		return macClipboardRead()
	}
	return linuxClipboardRead()
}

func clipboardWrite(text string) error {
	if runtime.GOOS == "darwin" {
		return macClipboardWrite(text)
	}
	return linuxClipboardWrite(text)
}

func macClipboardRead() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func macClipboardWrite(text string) error {
	cmd := exec.Command("pbcopy")
	w, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		return err
	}
	w.Close()
	return cmd.Wait()
}

func linuxClipboardRead() (string, error) {
	out, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
	if err == nil {
		return string(out), nil
	}
	out, err = exec.Command("xsel", "--clipboard", "--output").Output()
	return string(out), err
}

func linuxClipboardWrite(text string) error {
	cmd := exec.Command("xclip", "-selection", "clipboard")
	w, err := cmd.StdinPipe()
	if err != nil {
		cmd = exec.Command("xsel", "--clipboard", "--input")
		w, err = cmd.StdinPipe()
		if err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		return err
	}
	w.Close()
	return cmd.Wait()
}
