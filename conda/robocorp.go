package conda

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/robocorp/rcc/common"
	"github.com/robocorp/rcc/pathlib"
	"github.com/robocorp/rcc/shell"
	"github.com/robocorp/rcc/xviper"
)

var (
	ignoredPaths = []string{"python", "conda"}
	hashPattern  = regexp.MustCompile("^[0-9a-f]{16}(?:\\.meta)?$")
)

func sorted(files []os.FileInfo) {
	sort.SliceStable(files, func(left, right int) bool {
		return files[left].Name() < files[right].Name()
	})
}

func ignoreDynamicDirectories(folder, entryName string) bool {
	base := strings.ToLower(filepath.Base(folder))
	name := strings.ToLower(entryName)
	return name == "__pycache__" || (name == "gen" && base == "comtypes")
}

func DigestFor(folder string, collect map[string]string) ([]byte, error) {
	handle, err := os.Open(folder)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	entries, err := handle.Readdir(-1)
	if err != nil {
		return nil, err
	}
	digester := sha256.New()
	sorted(entries)
	for _, entry := range entries {
		if entry.IsDir() {
			if ignoreDynamicDirectories(folder, entry.Name()) {
				continue
			}
			digest, err := DigestFor(filepath.Join(folder, entry.Name()), collect)
			if err != nil {
				return nil, err
			}
			digester.Write(digest)
			continue
		}
		repr := fmt.Sprintf("%s -- %x", entry.Name(), entry.Size())
		digester.Write([]byte(repr))
	}
	result := digester.Sum([]byte{})
	if collect != nil {
		key := fmt.Sprintf("%02x", result)
		collect[folder] = key
	}
	return result, nil
}

func FindPath(environment string) pathlib.PathParts {
	target := pathlib.TargetPath()
	target = target.Remove(ignoredPaths)
	target = target.Prepend(CondaPaths(environment)...)
	return target
}

func EnvironmentExtensionFor(location string) []string {
	environment := make([]string, 0, 20)
	searchPath := FindPath(location)
	python, ok := searchPath.Which("python3", FileExtensions)
	if !ok {
		python, ok = searchPath.Which("python", FileExtensions)
	}
	if ok {
		environment = append(environment, "PYTHON_EXE="+python)
	}
	environment = append(environment,
		"CONDA_DEFAULT_ENV=rcc",
		"CONDA_PREFIX="+location,
		"CONDA_PROMPT_MODIFIER=(rcc) ",
		"CONDA_SHLVL=1",
		"PYTHONHOME=",
		"PYTHONSTARTUP=",
		"PYTHONEXECUTABLE=",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=x",
		"PYTHONPYCACHEPREFIX="+common.RobocorpTemp(),
		"ROBOCORP_HOME="+common.RobocorpHome(),
		"RCC_ENVIRONMENT_HASH="+common.EnvironmentHash,
		"RCC_INSTALLATION_ID="+xviper.TrackingIdentity(),
		"RCC_TRACKING_ALLOWED="+fmt.Sprintf("%v", xviper.CanTrack()),
		"TEMP="+common.RobocorpTemp(),
		"TMP="+common.RobocorpTemp(),
		searchPath.AsEnvironmental("PATH"),
	)
	environment = append(environment, LoadActivationEnvironment(location)...)
	return environment
}

func EnvironmentFor(location string) []string {
	return append(os.Environ(), EnvironmentExtensionFor(location)...)
}

func asVersion(text string) (uint64, string) {
	text = strings.TrimSpace(text)
	multiline := strings.SplitN(text, "\n", 2)
	if len(multiline) > 0 {
		text = strings.TrimSpace(multiline[0])
	}
	parts := strings.SplitN(text, ".", 4)
	steps := len(parts)
	multipliers := []uint64{1000000, 1000, 1}
	version := uint64(0)
	for at, multiplier := range multipliers {
		if steps <= at {
			break
		}
		value, err := strconv.ParseUint(parts[at], 10, 64)
		if err != nil {
			break
		}
		version += multiplier * value
	}
	return version, text
}

func MicromambaVersion() string {
	versionText, _, err := shell.New(CondaEnvironment(), ".", BinMicromamba(), "--repodata-ttl", "90000", "--version").CaptureOutput()
	if err != nil {
		return err.Error()
	}
	_, versionText = asVersion(versionText)
	return versionText
}

func HasMicroMamba() bool {
	if !pathlib.IsFile(BinMicromamba()) {
		return false
	}
	version, versionText := asVersion(MicromambaVersion())
	goodEnough := version >= 16000
	common.Debug("%q version is %q -> %v (good enough: %v)", BinMicromamba(), versionText, version, goodEnough)
	common.Timeline("µmamba version is %q (at %q).", versionText, BinMicromamba())
	return goodEnough
}

func LocalChannel() (string, bool) {
	basefolder := filepath.Join(common.RobocorpHome(), "channel")
	fullpath := filepath.Join(basefolder, "channeldata.json")
	stats, err := os.Stat(fullpath)
	if err != nil {
		return "", false
	}
	if !stats.IsDir() {
		return basefolder, true
	}
	return "", false
}
