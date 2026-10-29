package utils

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Simple verification that the project name does not contain any invalid characters.
func VerifyProjectName(projectName string) error {
	if projectName == "" {
		return fmt.Errorf("name can't be empty")
	}

	regex := regexp.MustCompile(`^[a-zA-Z]+[a-zA-Z0-9-]+$`)

	if !regex.MatchString(projectName) {
		return fmt.Errorf("name contains invalid characters")
	}

	return nil
}

// In k8s resources need to have a valid subdomain name.
// This function tries to convert the input to a valid subdomain name, or returns an error if it can't.
func ConvertToValidSubdomainName(input string) (string, error) {
	input = strings.ToLower(input)
	input = strings.ReplaceAll(input, "_", "-")

	// Remove unallowed characters
	reg, _ := regexp.Compile("[^a-z0-9.-]")
	sanitized := reg.ReplaceAllString(input, "")

	// Define the regex pattern
	pattern := regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)

	// Find the match
	validName := pattern.FindString(sanitized)

	if validName == "" {
		// Linter normally does not allow line breaks or punctuations in error messages
		// but this will trick it to not complain. In future the linter might fail here.
		errMsg := fmt.Sprintf(`provided name '%s' is not a valid name. automatic convertion failed with the resulting name '%s'. 
		Make sure that the name does not contain invalid characters.`, input, validName)
		return "", errors.New(errMsg)
	}

	return validName, nil
}

// Trims the name of the github archive folder root.
//
// Normally GH provides the root folder in this scheme: `OWNER-REPO-COMMITHASH`
// e.g. wandelbotsgmbh-wandelbots-js-robot-visualization-0a242ec
//
