package nativefixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func decodeConfig(data []byte) (modelConfig, error) {
	if len(data) > maxConfigBytes {
		return modelConfig{}, errors.New("configuration exceeds 16384 bytes")
	}
	if !utf8.Valid(data) {
		return modelConfig{}, errors.New("configuration is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return modelConfig{}, err
	}
	if token != json.Delim('{') {
		return modelConfig{}, errors.New("expected configuration object")
	}
	var config modelConfig
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return modelConfig{}, err
		}
		key, ok := token.(string)
		if !ok {
			return modelConfig{}, errors.New("expected configuration field name")
		}
		if seen[key] {
			return modelConfig{}, fmt.Errorf("duplicate field %q", key)
		}
		if key != "displayDefault" && key != "settlementSource" {
			return modelConfig{}, fmt.Errorf("unknown field %q", key)
		}
		seen[key] = true
		token, err = decoder.Token()
		if err != nil {
			return modelConfig{}, err
		}
		value, ok := token.(string)
		if !ok {
			return modelConfig{}, fmt.Errorf("field %q must be a string", key)
		}
		switch key {
		case "displayDefault":
			if value != "ORDER_CURRENCY" && value != "EUR" {
				return modelConfig{}, fmt.Errorf("invalid displayDefault %q", value)
			}
			config.displayDefault = displayDefault(value)
		case "settlementSource":
			if value != "ORDER_CURRENCY" && value != "DISPLAY_CURRENCY" {
				return modelConfig{}, fmt.Errorf("invalid settlementSource %q", value)
			}
			config.settlementSource = settlementSource(value)
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return modelConfig{}, err
	}
	if token != json.Delim('}') {
		return modelConfig{}, errors.New("expected closing configuration object")
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err != nil {
			return modelConfig{}, err
		}
		return modelConfig{}, errors.New("unexpected data after configuration object")
	}
	for _, key := range []string{"displayDefault", "settlementSource"} {
		if !seen[key] {
			return modelConfig{}, fmt.Errorf("missing field %q", key)
		}
	}
	return config, nil
}

func readConfigFile() (config modelConfig, err error) {
	entry, err := os.Lstat(ConfigFile)
	if err != nil {
		return modelConfig{}, err
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return modelConfig{}, fmt.Errorf("%s must not be a symlink", ConfigFile)
	}
	if !entry.Mode().IsRegular() {
		return modelConfig{}, fmt.Errorf("%s is not a regular file", ConfigFile)
	}
	file, err := os.Open(ConfigFile)
	if err != nil {
		return modelConfig{}, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return modelConfig{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return modelConfig{}, fmt.Errorf("%s changed while opening", ConfigFile)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return modelConfig{}, err
	}
	return decodeConfig(data)
}

func writeChild(writer io.Writer, text string) bool {
	n, err := io.WriteString(writer, text)
	return err == nil && n == len(text)
}

// DispatchChild handles only the exact private argument plus protocol marker.
// The marker selects a protocol, not an authenticated caller or a sandbox.
// Status 2 is abnormal output failure; status 3 is reserved for config errors.
func DispatchChild(args []string, marker string, stdout, stderr io.Writer) (code int, handled bool) {
	if len(args) != 1 || args[0] != ChildArg || marker != ChildEnvValue {
		return 0, false
	}
	config, err := readConfigFile()
	if err != nil {
		if !writeChild(stderr, "native fixture config: "+err.Error()+"\n") {
			return 2, true
		}
		return 3, true
	}
	const orderCurrency = "USD"
	result := completeRefund(orderCurrency, nil, config)
	preserved := result.settlementCurrency == orderCurrency
	outcome := "PREDICATE_FAIL"
	if preserved {
		outcome = "PREDICATE_PASS"
	}
	protocol := jsjson.Stringify(jsjson.MustObject(
		"protocol", jsjson.MakeString("faultline.witness-result.v1"),
		"outcome", jsjson.MakeString(outcome))) + "\n"
	if preserved {
		if !writeChild(stdout, "settlement currency preserved\n"+protocol) {
			return 2, true
		}
		return 0, true
	}
	if !writeChild(stdout, protocol) {
		return 2, true
	}
	if !writeChild(stderr, fmt.Sprintf("expected settlement %s; received %s\n", orderCurrency, result.settlementCurrency)) {
		return 2, true
	}
	return 1, true
}
