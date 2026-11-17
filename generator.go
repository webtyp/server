package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"webtyp.com/command"
)

// The code generator of an appkit app is webtyp.com/appkitc. Each app pins its version with a `tool` line in its go.mod
// and server only asks Go to run that version, so the templates always match the libraries the app chose.
const (
	// GeneratorModulePath is the module the app requires to get the generator.
	GeneratorModulePath = "webtyp.com/appkitc"
	// GeneratorToolPath is the package the app's go.mod lists in its `tool` line.
	GeneratorToolPath = "webtyp.com/appkitc/cmd/appkitc"
	// GeneratorToolName is what `go tool` is given to run it.
	GeneratorToolName = "appkitc"

	// The flags of the generator: server writes them and appkitc reads them, so both import the names from here.
	GeneratorFlagPort      = "port"
	GeneratorFlagPublicDir = "public-dir"
	GeneratorFlagPlainHTTP = "plain-http"

	goCommand     = "go"
	goGet         = "get"
	goTool        = "tool"
	goGetToolFlag = "-tool"
	versionLatest = "@latest"
)

var (
	// ErrGeneratorNotInstalled: the app's go.mod could not be given the generator.
	ErrGeneratorNotInstalled = errors.New("server: webtyp could not add the code generator (webtyp.com/appkitc) to this app's go.mod, so the app's main files were not written; nothing of yours was changed. Run `go get -tool webtyp.com/appkitc/cmd/appkitc@latest` in the app and read its error")
	// ErrGeneratorFailed: the generator ran and stopped.
	ErrGeneratorFailed = errors.New("server: the code generator (appkitc) stopped, so the app's main files may be old or missing; nothing of yours was changed. Run `go tool appkitc` in the app to see why")
	// ErrGeneratorNotUpgraded: the app could not take the newest generator.
	ErrGeneratorNotUpgraded = errors.New("server: the app could not take the newest code generator (webtyp.com/appkitc); nothing of yours was changed. Run `go get webtyp.com/appkitc@latest` in the app and read its error")
)

// RunGenerator writes the app's generated main files with the appkitc version its go.mod pins. The first time, when the
// go.mod has no `tool` line for it, it adds one (the newest version).
//
// Example: if err := server.RunGenerator(rootDir, server.MainConfig{Port: "8080", PublicDir: "web/public"}); err != nil { log(err) }
func RunGenerator(rootDir string, cfg MainConfig) error {
	if err := ensureGeneratorTool(rootDir); err != nil {
		return err
	}
	args := []string{
		goTool, GeneratorToolName,
		"-" + GeneratorFlagPort + "=" + cfg.Port,
		"-" + GeneratorFlagPublicDir + "=" + cfg.PublicDir,
	}
	if cfg.PlainHTTP {
		args = append(args, "-"+GeneratorFlagPlainHTTP)
	}
	if out, err := command.RunInDir(rootDir, goCommand, args...); err != nil {
		return fmt.Errorf("%w: %s", ErrGeneratorFailed, out)
	}
	return nil
}

// UpgradeGenerator takes the newest appkitc, and with it the library versions its templates call.
//
// Example: if err := server.UpgradeGenerator(rootDir); err != nil { log(err) }
func UpgradeGenerator(rootDir string) error {
	if out, err := command.RunInDir(rootDir, goCommand, goGet, GeneratorModulePath+versionLatest); err != nil {
		return fmt.Errorf("%w: %s", ErrGeneratorNotUpgraded, out)
	}
	return nil
}

func ensureGeneratorTool(rootDir string) error {
	content, err := os.ReadFile(filepath.Join(rootDir, goModFilename))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGeneratorNotInstalled, err)
	}
	if strings.Contains(string(content), GeneratorToolPath) {
		return nil
	}
	if out, err := command.RunInDir(rootDir, goCommand, goGet, goGetToolFlag, GeneratorToolPath+versionLatest); err != nil {
		return fmt.Errorf("%w: %s", ErrGeneratorNotInstalled, out)
	}
	return nil
}
