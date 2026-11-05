package utils

import (
	"fmt"
	"os"
	"strings"
)

// PromptUserConfirmation prompts the user with a message and returns true if the user confirms with 'y' or 'Y'.
func PromptUserConfirmation(message string) bool {
	fmt.Fprintf(os.Stderr, "%s (y/n): ", message)
	var response string
	fmt.Scanln(&response)
	return strings.ToLower(response) == "y"
}
