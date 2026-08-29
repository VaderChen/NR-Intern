package main

import (
	"bufio"
	"crypto/sha256"
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

func (value target) wixArchitecture() (string, error) {
	switch value.arch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("WiX 不支援 %s 架構", value.arch)
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

type msiMode string

const (
	msiRequired msiMode = "required"
	msiOptional msiMode = "optional"
	msiSkip     msiMode = "skip"
)

func main() {
	defaultVersion := releaseVersionAt(time.Now()).display
	version := flag.String("version", defaultVersion, "發行版本，格式為 1.YY.MMDD build HHmm")
	output := flag.String("output", "dist", "輸出目錄")
	targets := flag.String("targets", defaultTargets, "逗號分隔 GOOS/GOARCH")
	msi := flag.String("msi", string(msiRequired), "Windows MSI 模式：required、optional 或 skip")
	macIcon := flag.String("mac-icon", "", "macOS App 使用的 ICNS 圖示檔")
	windowsIcon := flag.String("windows-icon", "", "Windows MSI 使用的 ICO 圖示檔")
	flag.Parse()
	assets := releaseAssets{
		macIcon:     strings.TrimSpace(*macIcon),
		windowsIcon: strings.TrimSpace(*windowsIcon),
	}
	if err := build(strings.TrimSpace(*version), strings.TrimSpace(*output), *targets, msiMode(strings.TrimSpace(*msi)), assets); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(rawVersion, output, rawTargets string, windowsMSI msiMode, assetPaths releaseAssets) error {
	version, err := parseReleaseVersion(rawVersion)
	if err != nil {
		return err
	}
	if err := validateMSIMode(windowsMSI); err != nil {
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
			if err := buildWindowsInstaller(platformDirectory, version, value, windowsMSI, assets); err != nil {
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
	if sourceRoot == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return false, fmt.Errorf("解析 NetPassClient 預設來源目錄: %w", err)
		}
		sourceRoot = filepath.Join(workingDirectory, "..", "NetPassService", "Client", "bin")
	}
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return false, fmt.Errorf("解析 NetPassClient 來源目錄: %w", err)
	}
	source := filepath.Join(sourceRoot, sourceName)
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s 缺少 NetPassClient Runtime；請設定 NR_INTERN_NETPASS_SOURCE 指向外部預編譯檔目錄", value.directoryName())
	}
	destinationDirectory := filepath.Join(platformDirectory, "netpass-client")
	if err := os.MkdirAll(destinationDirectory, 0o750); err != nil {
		return false, fmt.Errorf("建立 %s NetPassClient 目錄: %w", value.directoryName(), err)
	}
	destinationName := "NetPassClient"
	if value.os == "windows" {
		destinationName += ".exe"
	}
	if err := copyFile(source, filepath.Join(destinationDirectory, destinationName), 0o755); err != nil {
		return false, fmt.Errorf("封裝 %s NetPassClient: %w", value.directoryName(), err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s NetPassClient runtime\n", value.directoryName())
	return true, nil
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
			command := exec.Command(codesign, "--force", "--deep", "--sign", "-", appDirectory)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			if err := command.Run(); err != nil {
				return fmt.Errorf("替 macOS App 套用 ad-hoc 簽章: %w", err)
			}
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s\n", appDirectory)
	return nil
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

func buildWindowsInstaller(platformDirectory string, version releaseVersion, value target, mode msiMode, assets releaseAssets) error {
	if mode == msiSkip {
		return nil
	}
	toolKind, toolPath := windowsInstallerTool()
	if toolPath == "" {
		message := "找不到 Windows MSI 封裝器；Windows 可安裝 WiX CLI，macOS／Linux 可安裝 msitools（wixl 與 msibuild）"
		if mode == msiOptional {
			_, _ = fmt.Fprintf(os.Stderr, "warning: %s，已保留 Windows 執行檔並略過 MSI\n", message)
			return nil
		}
		return fmt.Errorf("%s", message)
	}
	wixArch, err := value.wixArchitecture()
	if err != nil {
		return err
	}
	sourcePath := filepath.Join(platformDirectory, ".nr-intern-installer.wxs")
	source := windowsInstallerSource(platformDirectory, version, value, assets.windowsIcon)
	if toolKind == "wixl" {
		source = windowsInstallerSourceWiX3(platformDirectory, version, value, assets.windowsIcon)
	}
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		return fmt.Errorf("寫入 WiX 安裝描述: %w", err)
	}
	defer os.Remove(sourcePath)
	installerName := fmt.Sprintf("NR-Intern-%s-%s.msi", version.directory, value.directoryName())
	installerPath := filepath.Join(platformDirectory, installerName)
	var command *exec.Cmd
	if toolKind == "wixl" {
		// msitools 目前以 x64 模式建立所有 64 位元元件；ARM64 的 PE 檔案與
		// 64-bit Component 屬性相同，完成後再把 MSI Summary Template 正確標為 Arm64。
		command = exec.Command(toolPath, "-a", "x64", "-o", installerPath, sourcePath)
	} else {
		command = exec.Command(toolPath, "build", "-arch", wixArch, "-o", installerPath, sourcePath)
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("封裝 %s MSI: %w", value.directoryName(), err)
	}
	if toolKind == "wixl" && value.arch == "arm64" {
		msibuild, err := exec.LookPath("msibuild")
		if err != nil {
			return fmt.Errorf("ARM64 MSI 需要 msibuild 修正 Summary Template: %w", err)
		}
		command = exec.Command(msibuild, installerPath, "-s", "NR-Intern "+version.display, "NR-Intern", "Arm64;1033")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("設定 ARM64 MSI 平台資訊: %w", err)
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "packaged %s\n", installerPath)
	return nil
}

func windowsInstallerTool() (kind, path string) {
	if configured := strings.TrimSpace(os.Getenv("NR_INTERN_WIX")); configured != "" {
		return "wix", configured
	}
	if configured := strings.TrimSpace(os.Getenv("NR_INTERN_WIXL")); configured != "" {
		return "wixl", configured
	}
	// 官方 WiX CLI 只支援 Windows；Unix 優先使用專為交叉封裝設計的 msitools。
	if runtime.GOOS != "windows" {
		if found, err := exec.LookPath("wixl"); err == nil {
			return "wixl", found
		}
	}
	if found, err := exec.LookPath("wix"); err == nil {
		return "wix", found
	}
	if found, err := exec.LookPath("wixl"); err == nil {
		return "wixl", found
	}
	return "", ""
}

func windowsInstallerSource(platformDirectory string, version releaseVersion, value target, iconPath string) string {
	escape := html.EscapeString
	netPassDirectory, netPassFeature := windowsNetPassInstallerFragments(platformDirectory, true)
	upgradeCode := "8BFA111E-7AF2-45CE-A85E-270118822277"
	if value.arch == "arm64" {
		upgradeCode = "7F20A37E-76B5-48B4-BD68-F485EC6925B4"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
  <Package Name="NR-Intern" Manufacturer="NR-Intern" Version="%s" UpgradeCode="%s" Language="1033" Scope="perMachine" InstallerVersion="500" Compressed="yes">
    <SummaryInformation Description="NR-Intern %s" Manufacturer="NR-Intern" />
    <MajorUpgrade DowngradeErrorMessage="A newer version of NR-Intern is already installed." />
    <MediaTemplate EmbedCab="yes" />
    <Property Id="ARPCOMMENTS" Value="NR-Intern %s" />
    <Icon Id="ProductIcon" SourceFile="%s" />
    <Property Id="ARPPRODUCTICON" Value="ProductIcon" />
    <StandardDirectory Id="ProgramFiles64Folder">
      <Directory Id="INSTALLFOLDER" Name="NRIntern">
        <Component Id="DesktopExecutableComponent" Guid="*" Bitness="always64">
          <File Id="DesktopExecutable" Source="%s" KeyPath="yes" />
        </Component>
        <Component Id="ServerExecutableComponent" Guid="*" Bitness="always64">
          <File Id="ServerExecutable" Source="%s" KeyPath="yes" />
        </Component>
%s
      </Directory>
    </StandardDirectory>
    <Feature Id="ProductFeature" Title="NR-Intern" Level="1">
      <ComponentRef Id="DesktopExecutableComponent" />
      <ComponentRef Id="ServerExecutableComponent" />
%s
    </Feature>
  </Package>
</Wix>
`,
		escape(version.packageVersion),
		escape(upgradeCode),
		escape(version.display),
		escape(version.display),
		escape(iconPath),
		escape(filepath.Join(platformDirectory, "nr-intern-desktop.exe")),
		escape(filepath.Join(platformDirectory, "nr-intern-server.exe")),
		netPassDirectory,
		netPassFeature,
	)
}

func windowsInstallerSourceWiX3(platformDirectory string, version releaseVersion, value target, iconPath string) string {
	escape := html.EscapeString
	netPassDirectory, netPassFeature := windowsNetPassInstallerFragments(platformDirectory, false)
	upgradeCode := "{8BFA111E-7AF2-45CE-A85E-270118822277}"
	if value.arch == "arm64" {
		upgradeCode = "{7F20A37E-76B5-48B4-BD68-F485EC6925B4}"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*" Name="NR-Intern" Manufacturer="NR-Intern" Version="%s" Language="1033" UpgradeCode="%s">
    <Package InstallerVersion="500" Compressed="yes" InstallScope="perMachine" Description="NR-Intern %s" />
    <MajorUpgrade DowngradeErrorMessage="A newer version of NR-Intern is already installed." />
    <MediaTemplate EmbedCab="yes" />
    <Property Id="ARPCOMMENTS" Value="NR-Intern %s" />
    <Icon Id="ProductIcon" SourceFile="%s" />
    <Property Id="ARPPRODUCTICON" Value="ProductIcon" />
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLFOLDER" Name="NRIntern">
          <Component Id="DesktopExecutableComponent" Guid="*" Win64="yes">
            <File Id="DesktopExecutable" Source="%s" KeyPath="yes" />
          </Component>
          <Component Id="ServerExecutableComponent" Guid="*" Win64="yes">
            <File Id="ServerExecutable" Source="%s" KeyPath="yes" />
          </Component>
%s
        </Directory>
      </Directory>
    </Directory>
    <Feature Id="ProductFeature" Title="NR-Intern" Level="1">
      <ComponentRef Id="DesktopExecutableComponent" />
      <ComponentRef Id="ServerExecutableComponent" />
%s
    </Feature>
  </Product>
</Wix>
`,
		escape(version.packageVersion),
		escape(upgradeCode),
		escape(version.display),
		escape(version.display),
		escape(iconPath),
		escape(filepath.Join(platformDirectory, "nr-intern-desktop.exe")),
		escape(filepath.Join(platformDirectory, "nr-intern-server.exe")),
		netPassDirectory,
		netPassFeature,
	)
}

func windowsNetPassInstallerFragments(platformDirectory string, wix4 bool) (directory, feature string) {
	path := filepath.Join(platformDirectory, "netpass-client", "NetPassClient.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", ""
	}
	escapedPath := html.EscapeString(path)
	if wix4 {
		directory = fmt.Sprintf(`        <Directory Id="NetPassClientDirectory" Name="netpass-client">
          <Component Id="NetPassClientComponent" Guid="*" Bitness="always64">
            <File Id="NetPassClientExecutable" Source="%s" KeyPath="yes" />
          </Component>
        </Directory>`, escapedPath)
	} else {
		directory = fmt.Sprintf(`          <Directory Id="NetPassClientDirectory" Name="netpass-client">
            <Component Id="NetPassClientComponent" Guid="*" Win64="yes">
              <File Id="NetPassClientExecutable" Source="%s" KeyPath="yes" />
            </Component>
          </Directory>`, escapedPath)
	}
	return directory, `      <ComponentRef Id="NetPassClientComponent" />`
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
	return append(environment, "GOOS="+value.os, "GOARCH="+value.arch, "CGO_ENABLED="+cgo)
}

// nativeWindowSupported 判斷這個 target 的桌面程式能否內含原生視窗。
//
// window_darwin.go 的 build tag 是 `darwin && cgo`，而 cgo 無法在非 macOS 主機上
// 交叉建置 darwin 目標。若一律使用 CGO_ENABLED=0，發行的 macOS 二進位會永遠
// 退回開啟瀏覽器——功能只存在於本機建置，這種落差不能靜默發生。
func nativeWindowSupported(value target) bool {
	return value.os == "darwin" && runtime.GOOS == "darwin" && value.arch == runtime.GOARCH
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

func validateMSIMode(value msiMode) error {
	switch value {
	case msiRequired, msiOptional, msiSkip:
		return nil
	default:
		return fmt.Errorf("msi 模式必須是 required、optional 或 skip")
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
