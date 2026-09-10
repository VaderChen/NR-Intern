package main

import (
	"bufio"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/binary"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTargets = "windows/amd64,windows/arm64,darwin/arm64"
	versionPrefix  = "1"
)

var releaseVersionPattern = regexp.MustCompile(`^1\.(\d{2})\.(\d{2})(\d{2}) build (\d{2})(\d{2})$`)
var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
var developerIDIdentityPattern = regexp.MustCompile(`"(Developer ID Application:[^"]+)"`)

type target struct {
	os   string
	arch string
}

func (value target) directoryName() string {
	switch value.os {
	case "darwin":
		return "macos-" + value.arch
	case "windows":
		if value.arch == "amd64" {
			return "windows-x64"
		}
		return "windows-" + value.arch
	default:
		return value.os + "-" + value.arch
	}
}

type releaseVersion struct {
	display        string
	directory      string
	packageVersion string
	bundleVersion  string
	bundleBuild    string
}

type artifact struct {
	name   string
	sha256 string
}

type releaseAssets struct {
	macIcon     string
	windowsIcon string
}

type installerMode string

const (
	installerRequired installerMode = "required"
	installerOptional installerMode = "optional"
	installerSkip     installerMode = "skip"
)

func main() {
	defaultVersion := releaseVersionAt(time.Now()).display
	version := flag.String("version", defaultVersion, "發行版本，格式為 1.YY.MMDD build HHmm")
	output := flag.String("output", "dist", "輸出目錄")
	targets := flag.String("targets", defaultTargets, "逗號分隔 GOOS/GOARCH")
	installer := flag.String("installer", string(installerRequired), "Windows 安裝檔模式：required、optional 或 skip")
	macIcon := flag.String("mac-icon", "", "macOS App 使用的 ICNS 圖示檔")
	windowsIcon := flag.String("windows-icon", "", "Windows 安裝檔使用的 ICO 圖示檔")
	flag.Parse()
	assets := releaseAssets{
		macIcon:     strings.TrimSpace(*macIcon),
		windowsIcon: strings.TrimSpace(*windowsIcon),
	}
	if err := build(strings.TrimSpace(*version), strings.TrimSpace(*output), *targets, installerMode(strings.TrimSpace(*installer)), assets); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(rawVersion, output, rawTargets string, windowsInstaller installerMode, assetPaths releaseAssets) error {
	version, err := parseReleaseVersion(rawVersion)
	if err != nil {
		return err
	}
	if err := validateInstallerMode(windowsInstaller); err != nil {
		return err
	}
	values, err := parseTargets(rawTargets)
	if err != nil {
		return err
	}
	assets, err := resolveReleaseAssets(assetPaths)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("解析輸出目錄: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("建立輸出目錄: %w", err)
	}
	stage, err := os.MkdirTemp(root, ".nr-intern-release-")
	if err != nil {
		return fmt.Errorf("建立暫存發行目錄: %w", err)
	}
	defer os.RemoveAll(stage)

	releaseDirectory := filepath.Join(stage, version.directory)
	if err := os.MkdirAll(releaseDirectory, 0o750); err != nil {
		return fmt.Errorf("建立發行目錄: %w", err)
	}
	programs := []struct {
		name        string
		packagePath string
	}{
		{name: "nr-intern-server", packagePath: "./src/cmd/server"},
		{name: "nr-intern-desktop", packagePath: "./src/cmd/desktop"},
	}

	for _, value := range values {
		platformDirectory := filepath.Join(releaseDirectory, value.directoryName())
		if err := os.MkdirAll(platformDirectory, 0o750); err != nil {
			return fmt.Errorf("建立 %s 發行目錄: %w", value.directoryName(), err)
		}
		for _, program := range programs {
			name := program.name
			if value.os == "windows" {
				name += ".exe"
			}
			path := filepath.Join(platformDirectory, name)
			if err := buildProgram(program.packagePath, path, version.display, value); err != nil {
				return err
			}
			if err := os.Chmod(path, 0o755); err != nil {
				return fmt.Errorf("設定執行權限 %s: %w", path, err)
			}
			_, _ = fmt.Fprintf(os.Stdout, "built %s\n", path)
			if program.name == "nr-intern-desktop" && value.os == "darwin" && !nativeWindowSupported(value) {
				_, _ = fmt.Fprintf(os.Stderr,
					"warning: %s 不含原生視窗（需要在 %s/%s 主機上以 cgo 建置），啟動時會退回開啟瀏覽器\n",
					name, value.os, value.arch)
			}
		}
		netPassPackaged, err := stageNetPassClient(platformDirectory, value)
		if err != nil {
			return err
		}
		if !netPassPackaged {
			_, _ = fmt.Fprintf(os.Stderr, "warning: %s 尚無對應的 NetPassClient Runtime，反向代理頁面會顯示 Runtime 不可用\n", value.directoryName())
		}
		switch value.os {
		case "darwin":
			if err := buildMacApplication(platformDirectory, version, value, assets); err != nil {
				return err
			}
		case "windows":
			if err := buildWindowsInstaller(platformDirectory, version, value, windowsInstaller, assets); err != nil {
				return err
			}
		}
		if err := writeManifest(platformDirectory, platformDirectory); err != nil {
			return err
		}
	}

	if err := writeManifest(releaseDirectory, releaseDirectory); err != nil {
		return err
	}
	finalDirectory := filepath.Join(root, version.directory)
	if err := publishDirectory(releaseDirectory, finalDirectory); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "release %s\n", finalDirectory)
	return nil
}

func resolveReleaseAssets(paths releaseAssets) (releaseAssets, error) {
	resolve := func(label, path string) (string, error) {
		if path == "" {
			return "", fmt.Errorf("缺少 %s 路徑", label)
		}
		if filepath.IsAbs(path) || windowsAbsolutePathPattern.MatchString(path) || strings.HasPrefix(path, `\\`) {
			return "", fmt.Errorf("%s 必須使用相對路徑: %s", label, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("解析發行資產 %s: %w", path, err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return "", fmt.Errorf("讀取發行資產 %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("發行資產不是一般檔案: %s", path)
		}
		return absolute, nil
	}
	macIcon, err := resolve("macOS App 圖示", paths.macIcon)
	if err != nil {
		return releaseAssets{}, err
	}
	windowsIcon, err := resolve("Windows MSI 圖示", paths.windowsIcon)
	if err != nil {
		return releaseAssets{}, err
	}
	return releaseAssets{macIcon: macIcon, windowsIcon: windowsIcon}, nil
}

func buildProgram(packagePath, output, version string, value target) error {
	linkerFlags := buildLinkerFlags(packagePath, version, value)
	// 發行版本由 linker flags 明確注入；停用 Go VCS stamping，讓專案即使位於
	// 另一套版本控制工作副本內仍能以相同方式建置。
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags", linkerFlags, "-o", output, packagePath)
	command.Env = buildEnvironment(value)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("建置 %s/%s %s: %w", value.os, value.arch, packagePath, err)
	}
	return nil
}

func stageNetPassClient(platformDirectory string, value target) (bool, error) {
	sourceName := map[string]string{
		"darwin/arm64":  "NetPassClient_darwin_arm64",
		"linux/amd64":   "NetPassClient_linux_x64",
		"linux/arm64":   "NetPassClient_linux_arm64",
		"windows/amd64": "NetPassClient_windows_x64.exe",
	}[value.os+"/"+value.arch]
	if sourceName == "" {
		return false, nil
	}
	sourceRoot := strings.TrimSpace(os.Getenv("NR_INTERN_NETPASS_SOURCE"))
	explicitSource := sourceRoot != ""
	if sourceRoot == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return false, fmt.Errorf("解析 NetPassClient 預設來源目錄: %w", err)
		}
		sourceRoot = filepath.Join(workingDirectory, "..", "NetPassService", "Client")
	}
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return false, fmt.Errorf("解析 NetPassClient 來源目錄: %w", err)
	}
	destinationDirectory := filepath.Join(platformDirectory, "netpass-client")
	if err := os.MkdirAll(destinationDirectory, 0o750); err != nil {
		return false, fmt.Errorf("建立 %s NetPassClient 目錄: %w", value.directoryName(), err)
	}
	destinationName := "NetPassClient"
	if value.os == "windows" {
		destinationName += ".exe"
	}
	destination := filepath.Join(destinationDirectory, destinationName)
	moduleInfo, moduleErr := os.Stat(filepath.Join(sourceRoot, "go.mod"))
	if moduleErr == nil && moduleInfo.Mode().IsRegular() {
		// 輔助程式也屬於發行內容，不能沿用可能含有開發者目錄的舊 bin。
		// trimpath 保留套件相對的來源位置，堆疊仍可定位，且不需修改執行時路徑。
		command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", destination, ".")
		command.Dir = sourceRoot
		command.Env = buildEnvironment(value)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return false, fmt.Errorf("以套件相對路徑建置 %s NetPassClient: %w", value.directoryName(), err)
		}
		if err := validateNetPassBuild(destination, value); err != nil {
			return false, err
		}
	} else {
		if moduleErr != nil && !os.IsNotExist(moduleErr) {
			return false, fmt.Errorf("檢查 NetPassClient Go 專案: %w", moduleErr)
		}
		if moduleErr == nil {
			return false, fmt.Errorf("NetPassClient go.mod 必須是一般檔案")
		}
		if !explicitSource {
			sourceRoot = filepath.Join(sourceRoot, "bin")
		}
		source := filepath.Join(sourceRoot, sourceName)
		if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s 缺少 NetPassClient Runtime；請設定 NR_INTERN_NETPASS_SOURCE 指向 Go 專案或以 -trimpath 建置的預編譯檔目錄", value.directoryName())
		}
		// 外部預編譯檔同樣檢查，避免自訂來源繞過發行檔的路徑保護。
		if err := validateNetPassBuild(source, value); err != nil {
			return false, err
		}
		if err := copyFile(source, destination, 0o755); err != nil {
			return false, fmt.Errorf("封裝 %s NetPassClient: %w", value.directoryName(), err)
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s NetPassClient runtime\n", value.directoryName())
	return true, nil
}

func validateNetPassBuild(path string, value target) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("無法驗證 %s NetPassClient 建置資訊: %w", value.directoryName(), err)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["-trimpath"] != "true" {
		return fmt.Errorf("%s NetPassClient 未以 -trimpath 建置，拒絕封裝可能含開發者絕對路徑的檔案；請使用 Go 專案來源，或重新以 go build -buildvcs=false -trimpath 建置", value.directoryName())
	}
	if settings["GOOS"] != value.os || settings["GOARCH"] != value.arch {
		return fmt.Errorf("NetPassClient 架構 %s/%s 與目標 %s/%s 不符", settings["GOOS"], settings["GOARCH"], value.os, value.arch)
	}
	return nil
}

func buildLinkerFlags(packagePath, version string, value target) string {
	flags := fmt.Sprintf("-s -w -X 'AgenticService/src/bootstrap.Version=%s'", version)
	// Windows 桌面程式使用 GUI subsystem，避免從捷徑或 MSI 啟動時額外顯示
	// CMD 視窗；server 必須保留 console subsystem 以輸出診斷資訊。
	if value.os == "windows" && strings.HasSuffix(filepath.ToSlash(packagePath), "/desktop") {
		flags += " -H=windowsgui"
	}
	return flags
}

func buildMacApplication(platformDirectory string, version releaseVersion, value target, assets releaseAssets) error {
	if value.os != "darwin" {
		return fmt.Errorf("%s/%s 不是 macOS target", value.os, value.arch)
	}
	appDirectory := filepath.Join(platformDirectory, "NR-Intern.app")
	contentsDirectory := filepath.Join(appDirectory, "Contents")
	executableDirectory := filepath.Join(contentsDirectory, "MacOS")
	resourcesDirectory := filepath.Join(contentsDirectory, "Resources")
	if err := os.MkdirAll(executableDirectory, 0o750); err != nil {
		return fmt.Errorf("建立 macOS App 執行目錄: %w", err)
	}
	if err := os.MkdirAll(resourcesDirectory, 0o750); err != nil {
		return fmt.Errorf("建立 macOS App 資源目錄: %w", err)
	}
	desktopBinary := filepath.Join(platformDirectory, "nr-intern-desktop")
	appBinary := filepath.Join(executableDirectory, "NR-Intern")
	if err := copyFile(desktopBinary, appBinary, 0o755); err != nil {
		return fmt.Errorf("封裝 macOS App 執行檔: %w", err)
	}
	if err := copyFile(assets.macIcon, filepath.Join(resourcesDirectory, "AppIcon.icns"), 0o644); err != nil {
		return fmt.Errorf("封裝 macOS App 圖示: %w", err)
	}
	netPassSource := filepath.Join(platformDirectory, "netpass-client", "NetPassClient")
	if _, err := os.Stat(netPassSource); err == nil {
		netPassDirectory := filepath.Join(resourcesDirectory, "netpass-client")
		if err := os.MkdirAll(netPassDirectory, 0o750); err != nil {
			return fmt.Errorf("建立 macOS NetPassClient 資源目錄: %w", err)
		}
		if err := copyFile(netPassSource, filepath.Join(netPassDirectory, "NetPassClient"), 0o755); err != nil {
			return fmt.Errorf("封裝 macOS NetPassClient: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("檢查 macOS NetPassClient: %w", err)
	}
	plist := macInfoPlist(version)
	if err := os.WriteFile(filepath.Join(contentsDirectory, "Info.plist"), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("寫入 Info.plist: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contentsDirectory, "PkgInfo"), []byte("APPLNRIN"), 0o644); err != nil {
		return fmt.Errorf("寫入 PkgInfo: %w", err)
	}
	if runtime.GOOS == "darwin" {
		if codesign, err := exec.LookPath("codesign"); err == nil {
			identity, automatic, err := resolveMacSigningIdentity()
			if err != nil {
				return err
			}
			if err := signMacApp(codesign, identity, appDirectory); err != nil {
				return err
			}
			if identity == "-" {
				_, _ = fmt.Fprintln(os.Stderr,
					"warning: macOS App 使用 ad-hoc 簽章；重建後系統可能再次要求螢幕錄製授權。可設定 NR_INTERN_CODESIGN_IDENTITY 使用固定簽章身分")
			} else if automatic {
				_, _ = fmt.Fprintf(os.Stdout, "signed %s with automatically selected %s\n", appDirectory, identity)
			}
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s\n", appDirectory)
	return nil
}

// signMacApp 由內而外簽署 App Bundle。
//
// 先逐一簽署巢狀的 Mach-O 與 code bundle，最後才簽最外層——直接對整包下 --deep
// 會讓巢狀項目沿用外層設定，Apple 也明確不建議只靠 --deep。
//
// --options runtime 是重點：沒有 hardened runtime 就無法送公證。原本缺這個旗標，
// 產物雖然有 Developer ID 簽章，spctl 仍判為 "Unnotarized Developer ID" 而拒絕，
// 別台 Mac 打開會被 Gatekeeper 擋下。ad-hoc 簽章（identity "-"）不支援這些選項，
// 因此維持原本的簡單簽法。
func signMacApp(codesign, identity, appDirectory string) error {
	run := func(arguments ...string) error {
		command := exec.Command(codesign, arguments...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("替 macOS App 套用簽章 %q: %w", identity, err)
		}
		return nil
	}
	if identity == "-" {
		return run("--force", "--deep", "--sign", identity, appDirectory)
	}
	options := []string{"--options", "runtime", "--timestamp"}
	nested, err := nestedMachOFiles(filepath.Join(appDirectory, "Contents"))
	if err != nil {
		return err
	}
	mainExecutable, err := macMainExecutable(appDirectory)
	if err != nil {
		return err
	}
	for _, object := range nested {
		// 主執行檔交由最後的 App 簽章納入：先簽它會讓 codesign 提前回溯整包。
		if object == mainExecutable {
			continue
		}
		if err := run(append([]string{"--force", "--sign", identity}, append(options, object)...)...); err != nil {
			return err
		}
	}
	if err := run(append([]string{"--force", "--deep", "--sign", identity}, append(options, appDirectory)...)...); err != nil {
		return err
	}
	return run("--verify", "--deep", "--strict", "--verbose=2", appDirectory)
}

// nestedMachOFiles 找出 Bundle 內所有 Mach-O 執行檔。
func nestedMachOFiles(root string) ([]string, error) {
	objects := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		header := make([]byte, 4)
		count, readErr := io.ReadFull(file, header)
		_ = file.Close()
		if readErr != nil || count < 4 {
			return nil
		}
		if isMachOHeader(header) {
			objects = append(objects, path)
		}
		return nil
	})
	return objects, err
}

// isMachOHeader 認出 Mach-O 與 universal binary 的 magic number。
func isMachOHeader(header []byte) bool {
	magic := binary.BigEndian.Uint32(header)
	switch magic {
	case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, 0xcafebabe, 0xbebafeca:
		return true
	default:
		return false
	}
}

func macMainExecutable(appDirectory string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(appDirectory, "Contents", "MacOS"))
	if err != nil {
		return "", fmt.Errorf("讀取 macOS App 執行檔目錄: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return filepath.Join(appDirectory, "Contents", "MacOS", entry.Name()), nil
		}
	}
	return "", nil
}

// resolveMacSigningIdentity 優先採用明確設定；未設定時只自動選擇適合
// 站外發行的 Developer ID Application。固定簽章讓 macOS TCC 能在 App
// 重建後仍以相同指定需求辨識它，避免螢幕錄製等權限反覆失效。
func resolveMacSigningIdentity() (identity string, automatic bool, err error) {
	if configured, exists := os.LookupEnv("NR_INTERN_CODESIGN_IDENTITY"); exists {
		configured = strings.TrimSpace(configured)
		if configured == "" {
			return "", false, fmt.Errorf("NR_INTERN_CODESIGN_IDENTITY 不可為空；若要強制使用 ad-hoc 簽章請設定為 -")
		}
		return configured, false, nil
	}
	security, lookupErr := exec.LookPath("security")
	if lookupErr != nil {
		return "-", false, nil
	}
	output, commandErr := exec.Command(security, "find-identity", "-v", "-p", "codesigning").CombinedOutput()
	if commandErr != nil {
		return "", false, fmt.Errorf("查詢 macOS 程式簽章身分: %w", commandErr)
	}
	match := developerIDIdentityPattern.FindSubmatch(output)
	if len(match) < 2 {
		return "-", false, nil
	}
	return string(match[1]), true, nil
}

func macInfoPlist(version releaseVersion) string {
	escape := html.EscapeString
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleDisplayName</key>
  <string>NR-Intern</string>
  <key>CFBundleExecutable</key>
  <string>NR-Intern</string>
  <key>CFBundleGetInfoString</key>
  <string>%s</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon.icns</string>
  <key>CFBundleIdentifier</key>
  <string>com.nr-intern.agent</string>
  <key>CFBundleName</key>
  <string>NR-Intern</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>%s</string>
  <key>CFBundleVersion</key>
  <string>%s</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
`, escape(version.display), escape(version.bundleVersion), escape(version.bundleBuild))
}

// buildWindowsInstaller 產生 Windows 安裝檔。
//
// 用 NSIS 而不是 MSI：MSI 需要 WiX（只跑在 Windows）或 msitools 的 wixl，後者對
// ARM64 的支援要靠事後改寫 Summary Template 才勉強成立。NSIS 的 makensis 在 macOS
// 上是一級公民，同一份腳本兩種架構都產得出來，輸出也是使用者預期的 setup.exe。
//
// 腳本是靜態檔而不是這裡拼出來的字串：安裝流程有語系、架構檢查與檔案佔用處理，
// 那些東西寫成 Go 的字串樣板既難讀也難改，用 -D 傳參數就夠了。
func buildWindowsInstaller(platformDirectory string, version releaseVersion, value target, mode installerMode, assets releaseAssets) error {
	if mode == installerSkip {
		return nil
	}
	toolPath := windowsInstallerTool()
	scriptPath, scriptErr := windowsInstallerScriptPath()
	if toolPath == "" || scriptErr != nil {
		message := "找不到 Windows 安裝檔封裝器；請安裝 NSIS（macOS 執行 brew install nsis），或以 NR_INTERN_MAKENSIS 指定 makensis"
		if scriptErr != nil {
			message = scriptErr.Error()
		}
		if mode == installerOptional {
			_, _ = fmt.Fprintf(os.Stderr, "warning: %s，已保留 Windows 執行檔並略過安裝檔\n", message)
			return nil
		}
		return fmt.Errorf("%s", message)
	}
	installerName := fmt.Sprintf("NR-Intern-%s-%s-setup.exe", version.directory, value.directoryName())
	installerPath := filepath.Join(platformDirectory, installerName)
	// 先產在暫存目錄再搬進來：makensis 中途失敗會留下半個檔案，而發行流程
	// 只憑檔名存在就認定封裝成功。
	staging, err := os.MkdirTemp(platformDirectory, ".installer-")
	if err != nil {
		return fmt.Errorf("建立安裝檔暫存目錄: %w", err)
	}
	defer os.RemoveAll(staging)
	stagedPath := filepath.Join(staging, installerName)

	arguments := []string{
		"-V2",
		"-DPAYLOAD_DIR=" + platformDirectory,
		"-DOUTPUT_FILE=" + stagedPath,
		"-DAPP_VERSION=" + version.display,
		"-DNUMERIC_VERSION=" + version.packageVersion + ".0",
		"-DAPP_ARCH=" + value.arch,
		"-DINSTALLER_ICON=" + assets.windowsIcon,
	}
	if windowsNetPassPayload(platformDirectory) != "" {
		arguments = append(arguments, "-DWITH_NETPASS=1")
	}
	command := exec.Command(toolPath, append(arguments, scriptPath)...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("封裝 %s 安裝檔: %w", value.directoryName(), err)
	}
	if err := os.Rename(stagedPath, installerPath); err != nil {
		return fmt.Errorf("搬移 %s 安裝檔: %w", value.directoryName(), err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s\n", installerPath)
	return nil
}

func windowsInstallerTool() string {
	if configured := strings.TrimSpace(os.Getenv("NR_INTERN_MAKENSIS")); configured != "" {
		return configured
	}
	if found, err := exec.LookPath("makensis"); err == nil {
		return found
	}
	return ""
}

func windowsInstallerScriptPath() (string, error) {
	path, err := filepath.Abs(filepath.Join("scripts", "windows-installer.nsi"))
	if err != nil {
		return "", fmt.Errorf("解析安裝檔腳本路徑: %w", err)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("找不到安裝檔腳本 %s；請從專案根目錄執行", path)
	}
	return path, nil
}

// windowsNetPassPayload 回傳要一併安裝的 NetPass Client；沒有就回空字串。
func windowsNetPassPayload(platformDirectory string) string {
	path := filepath.Join(platformDirectory, "netpass-client", "NetPassClient.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}

// windowsToolchainRoot 是可攜式 LLVM-MinGW 的預設位置。
//
// 交叉編譯 Windows 的 cgo 需要 MinGW，而 Homebrew 只提供 x86_64；ARM64 要靠
// LLVM-MinGW 這種同時涵蓋兩種架構的可攜式工具鏈。路徑只存在於開發機，
// 不會進入發行套件。
func windowsToolchainRoot() string {
	if configured := strings.TrimSpace(os.Getenv("NR_INTERN_LLVM_MINGW")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "yourdesk", "toolchains", "llvm-mingw")
}

// windowsTool 找出指定架構的 MinGW 工具；PATH 優先，其次才是可攜式工具鏈。
func windowsTool(arch, kind string) string {
	if configured := strings.TrimSpace(os.Getenv(fmt.Sprintf("NR_INTERN_%s_WINDOWS_%s", strings.ToUpper(kind), strings.ToUpper(arch)))); configured != "" {
		return configured
	}
	prefix := "x86_64"
	if arch == "arm64" {
		prefix = "aarch64"
	}
	suffixes := map[string][]string{
		"cc":      {"gcc", "clang"},
		"cxx":     {"g++", "clang++"},
		"windres": {"windres"},
	}[strings.ToLower(kind)]
	names := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		names = append(names, fmt.Sprintf("%s-w64-mingw32-%s", prefix, suffix))
	}
	for _, name := range names {
		if found, err := exec.LookPath(name); err == nil {
			return found
		}
	}
	root := windowsToolchainRoot()
	if root == "" {
		return ""
	}
	for _, name := range names {
		candidate := filepath.Join(root, "bin", name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func buildEnvironment(value target) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(name) {
		case "GOOS", "GOARCH", "CGO_ENABLED":
			continue
		}
		environment = append(environment, item)
	}
	cgo := "0"
	if nativeWindowSupported(value) {
		cgo = "1"
	}
	environment = append(environment, "GOOS="+value.os, "GOARCH="+value.arch, "CGO_ENABLED="+cgo)
	// 交叉編譯 Windows 的 cgo 必須明確指定編譯器；Go 預設會找主機的 gcc，
	// 那是 macOS 的 clang，產不出 PE 目標檔。
	if cgo == "1" && value.os == "windows" {
		if compiler := windowsTool(value.arch, "cc"); compiler != "" {
			environment = append(environment, "CC="+compiler)
		}
		if compiler := windowsTool(value.arch, "cxx"); compiler != "" {
			environment = append(environment, "CXX="+compiler)
		}
	}
	return environment
}

// nativeWindowSupported 判斷這個 target 的桌面程式能否內含原生視窗。
//
// window_darwin.go 的 build tag 是 `darwin && cgo`，而 cgo 無法在非 macOS 主機上
// 交叉建置 darwin 目標。若一律使用 CGO_ENABLED=0，發行的 macOS 二進位會永遠
// 退回開啟瀏覽器——功能只存在於本機建置，這種落差不能靜默發生。
func nativeWindowSupported(value target) bool {
	if value.os == "darwin" {
		// Cocoa 的 WebKit 綁定只能在 macOS 主機上以相同架構編譯。
		return runtime.GOOS == "darwin" && value.arch == runtime.GOARCH
	}
	if value.os == "windows" {
		// WebView2 綁定同樣要 cgo，但可以交叉編譯——只要找得到該架構的 MinGW。
		// 找不到就退回無視窗版本：那個版本仍然可用，只是開的是瀏覽器。
		return windowsTool(value.arch, "cc") != "" && windowsTool(value.arch, "cxx") != ""
	}
	return false
}

func parseTargets(raw string) ([]target, error) {
	allowedOS := map[string]struct{}{"darwin": {}, "linux": {}, "windows": {}}
	allowedArch := map[string]struct{}{"amd64": {}, "arm64": {}}
	result := []target{}
	seen := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.Split(strings.TrimSpace(item), "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("無效 target %q，格式必須是 GOOS/GOARCH", item)
		}
		value := target{os: strings.ToLower(parts[0]), arch: strings.ToLower(parts[1])}
		if _, ok := allowedOS[value.os]; !ok {
			return nil, fmt.Errorf("不支援 GOOS %q", value.os)
		}
		if _, ok := allowedArch[value.arch]; !ok {
			return nil, fmt.Errorf("不支援 GOARCH %q", value.arch)
		}
		key := value.os + "/" + value.arch
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("至少需要一個 build target")
	}
	return result, nil
}

func releaseVersionAt(value time.Time) releaseVersion {
	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	value = value.In(taipei)
	display := fmt.Sprintf("%s.%02d.%02d%02d build %02d%02d", versionPrefix, value.Year()%100, value.Month(), value.Day(), value.Hour(), value.Minute())
	version, err := parseReleaseVersion(display)
	if err != nil {
		panic(err)
	}
	return version
}

func parseReleaseVersion(value string) (releaseVersion, error) {
	matches := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 6 {
		return releaseVersion{}, fmt.Errorf("version 必須符合 1.YY.MMDD build HHmm，例如 1.26.0828 build 1430")
	}
	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])
	hour, _ := strconv.Atoi(matches[4])
	minute, _ := strconv.Atoi(matches[5])
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 {
		return releaseVersion{}, fmt.Errorf("version 含有無效的日期或時間: %q", value)
	}
	date := time.Date(2000+year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
	if int(date.Month()) != month || date.Day() != day {
		return releaseVersion{}, fmt.Errorf("version 含有無效日期: %q", value)
	}
	monthDay := month*100 + day
	display := fmt.Sprintf("1.%02d.%02d%02d build %02d%02d", year, month, day, hour, minute)
	return releaseVersion{
		display:        display,
		directory:      strings.Replace(display, " build ", "-build-", 1),
		packageVersion: fmt.Sprintf("1.%d.%d", year, monthDay),
		bundleVersion:  fmt.Sprintf("1.%02d.%02d%02d", year, month, day),
		bundleBuild:    fmt.Sprintf("%02d.%02d%02d.%02d%02d", year, month, day, hour, minute),
	}, nil
}

func validateInstallerMode(value installerMode) error {
	switch value {
	case installerRequired, installerOptional, installerSkip:
		return nil
	default:
		return fmt.Errorf("installer 模式必須是 required、optional 或 skip")
	}
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func fileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func writeManifest(scanRoot, manifestDirectory string) error {
	artifacts := []artifact{}
	err := filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == "SHA256SUMS" {
			return nil
		}
		checksum, err := fileChecksum(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(scanRoot, path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{name: filepath.ToSlash(name), sha256: checksum})
		return nil
	})
	if err != nil {
		return fmt.Errorf("計算 checksum: %w", err)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].name < artifacts[j].name })
	manifest := filepath.Join(manifestDirectory, "SHA256SUMS")
	file, err := os.OpenFile(manifest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("建立 checksum manifest: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, value := range artifacts {
		if _, err := fmt.Fprintf(writer, "%s  %s\n", value.sha256, value.name); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "manifest %s\n", manifest)
	return nil
}

func publishDirectory(source, destination string) error {
	backup := ""
	if _, err := os.Stat(destination); err == nil {
		backup = fmt.Sprintf("%s.previous-%d", destination, time.Now().UnixNano())
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("暫存既有發行目錄: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("檢查既有發行目錄: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("發布發行目錄: %w", err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("移除舊發行目錄: %w", err)
		}
	}
	return nil
}
