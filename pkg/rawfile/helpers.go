package rawfile

import (
	"fmt"
	"log"
	"os/exec"
)

// Helper: run command and return error only
func execCommandSimple(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(out))
	}
	return nil
}

// Helper: find loop device for a mount point
func FindLoopDevice(target string) (string, error) {
	out, err := exec.Command("mount").CombinedOutput()
	if err != nil {
		return "", err
	}
	lines := SplitLines(string(out))
	for _, line := range lines {
		if len(line) > 0 && Contains(line, target) && Contains(line, "/dev/loop") {
			fields := SplitFields(line)
			if len(fields) > 0 {
				return fields[0], nil
			}
		}
	}
	return "", nil
}

// Helper: split string into lines
func SplitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// Helper: split string into fields
func SplitFields(s string) []string {
	var fields []string
	field := ""
	for _, c := range s {
		if c == ' ' || c == '\t' {
			if field != "" {
				fields = append(fields, field)
				field = ""
			}
		} else {
			field += string(c)
		}
	}
	if field != "" {
		fields = append(fields, field)
	}
	return fields
}

// Helper: check if substring is in string (naive)
func Contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && (s[0:len(substr)] == substr || Contains(s[1:], substr))))
}

// Helper: run command and return output
func execCommand(name string, args ...string) ([]byte, error) {
	log.Printf("execCommand: %s %v", name, args)
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}
