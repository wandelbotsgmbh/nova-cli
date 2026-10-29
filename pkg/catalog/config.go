package catalog

import (
	"errors"
	neturl "net/url"
	"regexp"
	"slices"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	pathInYaml = "catalog"
	// when chaning also change the default value the error message
	nameLength = 100
)

var (
	ErrNameAlreadyExists = errors.New("entry with name already exists")
	ErrNameEmpty         = errors.New("no empty names allowed")
	ErrNameInvalidChars  = errors.New("name contains invalid characters or starts with a number")
	ErrNameLenght        = errors.New("name is too long (max 100)")
	ErrUrlSchemeInvalid  = errors.New("url scheme must be http or https")
	ErrUrlHostMissing    = errors.New("url must contain a host")
)

type Entry struct {
	Name string `yaml:"name"`
	Url  string `yaml:"url"`
}

type Config struct {
	Entries []Entry `yaml:"hosts"`
}

// Reads the config for the catalog
func ReadConfig() (*Config, error) {
	config := &Config{}
	mapped := viper.GetStringMap(pathInYaml)
	plain, _ := yaml.Marshal(mapped)
	err := yaml.Unmarshal(plain, config)
	return config, err
}

// Adds and persists a new catalog configuration
func AddCatalog(name string, host string) error {
	// TODO validation
	config, err := ReadConfig()
	if err != nil {
		return err
	}

	for _, entry := range config.Entries {
		if entry.Name == name {
			return ErrNameAlreadyExists
		}
	}

	err = ValidateName(name)
	if err != nil {
		return err
	}
	err = ValidateUrl(host)
	if err != nil {
		return err
	}

	if host[len(host)-1] != '/' {
		host = host + "/"
	}

	config.Entries = append(config.Entries, Entry{Name: name, Url: host})
	viper.Set(pathInYaml, config)
	return viper.WriteConfig()
}

// Removes a catalog configuration
func RemoveCatalog(name string) error {
	config, err := ReadConfig()
	if err != nil {
		return err
	}
	for i, entry := range config.Entries {
		if entry.Name == name {
			config.Entries = slices.Delete(config.Entries, i, i+1)
			break
		}
	}
	viper.Set(pathInYaml, config)
	return viper.WriteConfig()
}

// Only Simple names are allowed for the catalog
func ValidateName(name string) error {
	if name == "" {
		return ErrNameEmpty
	}

	if len(name) > nameLength {
		return ErrNameLenght
	}

	validName := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,99}$`)
	if !validName.MatchString(name) {
		return ErrNameInvalidChars
	}
	return nil
}

// ValidateName checks if the name is valid
func ValidateUrl(url string) error {
	parsed, err := neturl.Parse(url)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrUrlSchemeInvalid
	}

	if parsed.Host == "" {
		return ErrUrlHostMissing
	}
	return err
}
