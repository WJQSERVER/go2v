package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// goVersionURL Go 官方下载页面 JSON API 的 URL，获取所有 Go 版本信息
	goVersionURL = "https://go.dev/dl/?mode=json"
	// latestVersionTextURL Go 官方提供最新版本号的纯文本 URL
	latestVersionTextURL = "https://go.dev/VERSION?m=text"
	// systemProfileDDirextory 系统全局 PATH 配置目录
	systemProfileDDirextory = "/etc/profile.d"
	// systemGoProfileFilename 系统全局 Go PATH 配置文件名
	systemGoProfileFilename = "go.sh"
)

// GoVersionInfo 表示 Go 版本信息 JSON 响应中的单个版本条目
type GoVersionInfo struct {
	Version string     `json:"version"` // Version Go 版本号 (例如 "go1.22.2")
	Stable  bool       `json:"stable"`  // Stable 表明是否是稳定版本
	Files   []struct { // Files 该版本对应的文件列表
		Filename string `json:"filename"` // Filename 文件名 (例如 "go1.22.2.linux-amd64.tar.gz")
		OS       string `json:"os"`       // OS 操作系统 (例如 "linux", "darwin", "windows")
		Arch     string `json:"arch"`     // Arch 架构 (例如 "amd64", "arm64")
		Checksum string `json:"checksum"` // Checksum 文件的校验和
		Size     int    `json:"size"`     // Size 文件大小
		Kind     string `json:"kind"`     // Kind 文件类型 (例如 "archive", "pkg")
	} `json:"files"`
}

var (
	// targetVersions 存储用户通过 -v 指定的 Go 版本列表
	targetVersions listArgs
	// debugMode 控制是否启用调试输出
	debugMode bool
	// rootMode 控制是否尝试以 root 权限进行全局 PATH 配置
	rootMode bool
)

// listArgs 自定义的 flag 类型，接收多个 -v 参数
type listArgs []string

// String 方法用于 flag 包打印默认值时的格式化
func (l *listArgs) String() string {
	return strings.Join(*l, ", ")
}

// Set 方法用于 flag 包解析命令行参数
func (l *listArgs) Set(value string) error {
	*l = append(*l, value)
	return nil
}

// init 函数在 main 之前执行，初始化命令行 flag
func init() {
	// 注册 -v flag
	flag.Var(&targetVersions, "v", "Specify the Go version to install (e.g., 1.22.2, 1.23). Can be specified multiple times.")
	// 注册 --debug flag
	flag.BoolVar(&debugMode, "debug", false, "Enable debug mode for verbose output.")
	// 注册 --root flag
	flag.BoolVar(&rootMode, "root", false, "Attempt to configure PATH globally with root privileges.")
}

// debugPrint 在调试模式下打印信息
func debugPrint(format string, a ...interface{}) {
	if debugMode {
		fmt.Printf("Debug: "+format+"\n", a...)
	}
}

// main 函数程序入口点
func main() {
	// 解析命令行参数
	flag.Parse()

	debugPrint("Debug mode enabled")

	fmt.Println("Starting GO environment installation (rootless by default)")

	// 获取系统信息（内核版本和架构）
	kernelVersion, detectedArchitecture, err := getSystemInfo()
	var goArch string
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get system information: %v\n", err)
		fmt.Printf("Warning: Will use Go's build time system and architecture (%s/%s)\n", runtime.GOOS, runtime.GOARCH)
		goArch = runtime.GOARCH
	} else {
		fmt.Printf("System Info: Kernel Version %s, Detected Architecture %s\n", kernelVersion, detectedArchitecture)
		goArch = mapArchitecture(detectedArchitecture)
		if goArch == "" {
			fmt.Fprintf(os.Stderr, "Error: Could not map detected architecture '%s' to a supported Go architecture.\n", detectedArchitecture)
			os.Exit(1)
		}
		fmt.Printf("Mapped Go Architecture: %s\n", goArch)
	}

	// 获取当前用户主目录路径
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get user home directory: %v\n", err)
		os.Exit(1)
	}
	// installPath Go 安装路径（用户主目录下的 .local/go）
	installPath := filepath.Join(homeDir, ".local", "go")
	fmt.Printf("Installation path set to: %s\n", installPath)

	// 获取所有 Go 版本信息列表（从 JSON API）
	debugPrint("Fetching all Go version information from JSON API...")
	allVersions, err := getAllGoVersions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get Go version list from JSON API: %v\n", err)
	}
	if allVersions != nil {
		debugPrint("Fetched %d Go versions from JSON API", len(allVersions))
	}

	// versionToInstall 最终确定的版本号
	// downloadURL 最终确定的下载 URL
	var versionToInstall, downloadURL string
	foundDownloadable := false

	if len(targetVersions) > 0 {
		debugPrint("Target versions specified: %v", targetVersions)
		for _, targetVer := range targetVersions {
			originalTargetVer := targetVer
			// 补全简略版本号
			if count := strings.Count(targetVer, "."); count == 1 {
				targetVer = targetVer + ".0"
				debugPrint("Appended .0 to version: %s -> %s", originalTargetVer, targetVer)
			}

			// 在 JSON API 数据中查找匹配版本
			if allVersions != nil {
				for _, v := range allVersions {
					if strings.TrimPrefix(v.Version, "go") == targetVer {
						debugPrint("Found matching version in JSON list: %s", v.Version)
						// 查找适用于当前 OS 和架构的 archive 文件
						for _, file := range v.Files {
							if file.OS == runtime.GOOS && file.Arch == goArch && file.Kind == "archive" {
								versionToInstall = strings.TrimPrefix(v.Version, "go")
								downloadURL = fmt.Sprintf("https://go.dev/dl/%s", file.Filename)
								foundDownloadable = true
								debugPrint("Found matching download file for %s/%s: %s", runtime.GOOS, goArch, file.Filename)
								break
							} else {
								debugPrint("Skipping file %s (OS: %s, Arch: %s), expected %s/%s", file.Filename, file.OS, file.Arch, runtime.GOOS, goArch)
							}
						}
					}
					if foundDownloadable {
						break
					}
				}
			}

			if foundDownloadable {
				break
			} else {
				fmt.Printf("Warning: Could not find specified version %s (%s/%s) in JSON API. Attempting to construct URL...\n", originalTargetVer, runtime.GOOS, goArch)
				versionToInstall = targetVer
				downloadURL = fmt.Sprintf("https://go.dev/dl/go%s.%s-%s.tar.gz", versionToInstall, runtime.GOOS, goArch)
				fmt.Printf("Attempting to construct download URL: %s\n", downloadURL)
				foundDownloadable = true
				break
			}
		}

		if !foundDownloadable {
			fmt.Fprintf(os.Stderr, "Error: No matching Go version found for installation. Please check the version number and system architecture.\n")
			os.Exit(1)
		}

	} else {
		// 未指定版本，查找最新稳定版本
		debugPrint("No target version specified")

		// 优先从 JSON API 获取最新稳定版本
		if allVersions != nil {
			debugPrint("Looking for latest stable version in JSON API")
			for _, v := range allVersions {
				if v.Stable {
					debugPrint("Checking stable version: %s", v.Version)
					// 查找适用于当前 OS 和架构的 archive 文件
					for _, file := range v.Files {
						if file.OS == runtime.GOOS && file.Arch == goArch && file.Kind == "archive" {
							versionToInstall = strings.TrimPrefix(v.Version, "go")
							downloadURL = fmt.Sprintf("https://go.dev/dl/%s", file.Filename)
							foundDownloadable = true
							debugPrint("Found latest stable download file for %s/%s: %s", runtime.GOOS, goArch, file.Filename)
							break
						} else {
							debugPrint("Skipping file %s (OS: %s, Arch: %s), expected %s/%s", file.Filename, file.OS, file.Arch, runtime.GOOS, goArch)
						}
					}
				} else {
					debugPrint("Skipping non-stable version: %s", v.Version)
				}
				if foundDownloadable {
					break
				}
			}
		}

		// 如果 JSON API 没找到，尝试从文本接口获取最新版本号
		if !foundDownloadable || downloadURL == "" {
			fmt.Println("Warning: Could not find latest stable version in JSON API. Attempting to get latest version from go.dev/VERSION?m=text using HTTP request...")
			latestVer, err := getLatestGoVersionFromTextHTTP()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to get latest Go version from text URL via HTTP: %v\n", err)
				fmt.Fprintf(os.Stderr, "Error: Could not determine Go version to install.\n")
				os.Exit(1)
			}
			versionToInstall = latestVer
			downloadURL = fmt.Sprintf("https://go.dev/dl/go%s.%s-%s.tar.gz", versionToInstall, runtime.GOOS, goArch)
			fmt.Printf("Deduced latest version: %s, Constructed download URL: %s\n", versionToInstall, downloadURL)
			foundDownloadable = true
		}

		if !foundDownloadable {
			fmt.Fprintf(os.Stderr, "Error: Internal error: Failed to determine version and download URL.\n")
			os.Exit(1)
		}
		fmt.Printf("No version specified, installing latest stable version: %s\n", versionToInstall)
	}

	fmt.Printf("Confirmed download URL: %s\n", downloadURL)

	// 下载 Go 安装包
	fmt.Printf("Downloading installation package...\n")
	downloadFileName := filepath.Base(downloadURL)
	downloadFilePath := filepath.Join(os.TempDir(), downloadFileName)

	debugPrint("Download file name: %s", downloadFileName)
	debugPrint("Download file path: %s", downloadFilePath)

	if downloadFileName == "." || downloadFileName == "" {
		fmt.Fprintf(os.Stderr, "Error: Invalid download URL or file name extraction failed. Download URL: %s\n", downloadURL)
		os.Exit(1)
	}

	// 检查临时目录
	tempDir := os.TempDir()
	debugPrint("Checking temporary directory: %s", tempDir)

	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		debugPrint("Temporary directory does not exist, creating...")
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create temporary directory: %v\n", err)
			os.Exit(1)
		}
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to check temporary directory: %v\n", err)
		os.Exit(1)
	}

	// 检查临时目录是否可写
	testFile := filepath.Join(tempDir, "test_write")
	debugPrint("Checking write permissions in temporary directory: %s", testFile)
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Temporary directory %s is not writable. Please check permissions.\n", tempDir)
		os.Exit(1)
	}
	os.Remove(testFile)
	debugPrint("Temporary directory is writable")

	// 执行文件下载
	err = downloadFile(downloadURL, downloadFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to download installation package: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Installation package downloaded successfully: %s\n", downloadFilePath)

	// 清理旧的 Go 安装目录
	fmt.Printf("Cleaning up old installation directory (if exists)....\n")
	cleanupPath := installPath

	// 如果设置了 --root flag 且具有 root 权限，则清理 /usr/local/go
	if rootMode && os.Geteuid() == 0 {
		cleanupPath = "/usr/local/go"
		installPath = "/usr/local/go"
		debugPrint("Root mode enabled and has root privileges. Cleaning up global installation path: %s", cleanupPath)
		debugPrint("Setting global installation path to: %s", installPath)
	} else {
		debugPrint("Cleaning up user installation path: %s", cleanupPath)
	}

	debugPrint("Checking installation path for cleanup: %s", cleanupPath)
	if _, err := os.Stat(cleanupPath); !os.IsNotExist(err) {
		debugPrint("Old installation directory found, removing...")
		err = os.RemoveAll(cleanupPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to clean up old installation directory: %v\n", err)
		} else {
			debugPrint("Old installation directory cleaned up")
		}
	} else {
		debugPrint("Old installation directory not found, skipping cleanup")
	}

	// 解压 Go 安装包
	fmt.Printf("Extracting installation package to %s...\n", installPath)
	extractDestDir := filepath.Join(homeDir, ".local")

	// 如果设置了 --root flag 且具有 root 权限，则解压到 /usr/local
	if rootMode && os.Geteuid() == 0 {
		extractDestDir = "/usr/local"
		debugPrint("Root mode enabled and has root privileges. Extracting to global directory: %s", extractDestDir)
	} else {
		debugPrint("Extracting to user directory: %s", extractDestDir)
	}

	debugPrint("Extracting %s to %s", downloadFilePath, extractDestDir)
	err = extractTarGz(downloadFilePath, extractDestDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to extract installation package: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Extraction complete\n")

	// 清理下载的 Go 安装包文件
	fmt.Printf("Cleaning up downloaded installation package...\n")
	debugPrint("Removing downloaded file: %s", downloadFilePath)
	err = os.Remove(downloadFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean up installation package: %v\n", err)
	} else {
		fmt.Printf("Installation package cleaned up\n")
	}

	// 配置 PATH 环境变量
	goBinPath := filepath.Join(installPath, "bin")

	// 检查是否在 root 模式下并且具有 root 权限
	if rootMode && os.Geteuid() == 0 {
		fmt.Println("Attempting to configure PATH globally...")
		systemGoProfilePath := filepath.Join(systemProfileDDirextory, systemGoProfileFilename)
		exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", goBinPath)

		// 检查 /etc/profile.d 目录是否存在
		if _, err := os.Stat(systemProfileDDirextory); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Directory %s does not exist. Cannot configure PATH globally.\n", systemProfileDDirextory)
			fmt.Println("Falling back to user configuration...")
			configureUserPath(homeDir, installPath)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to check directory %s: %v\n", systemProfileDDirextory, err)
			fmt.Println("Falling back to user configuration...")
			configureUserPath(homeDir, installPath)
		} else {
			// 检查 /etc/profile.d/go.sh 是否存在
			_, err := os.Stat(systemGoProfilePath)
			if os.IsNotExist(err) {
				// 如果文件不存在，创建并写入 PATH 行
				fmt.Printf("%s not found, creating %s...\n", systemGoProfileFilename, systemGoProfilePath)
				file, createErr := os.Create(systemGoProfilePath)
				if createErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: Failed to create %s: %v\n", systemGoProfilePath, createErr)
					fmt.Println("Falling back to user configuration...")
					configureUserPath(homeDir, installPath)
				} else {
					defer file.Close()
					_, writeErr := file.WriteString(exportLine + "\n")
					if writeErr != nil {
						fmt.Fprintf(os.Stderr, "Warning: Failed to write to %s: %v\n", systemGoProfilePath, writeErr)
						fmt.Println("Falling back to user configuration...")
						configureUserPath(homeDir, installPath)
					} else {
						fmt.Printf("Added '%s' to %s.\n", exportLine, systemGoProfilePath)
						printGlobalActivationInstruction(systemGoProfilePath)
					}
				}
			} else if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to check %s: %v\n", systemGoProfilePath, err)
				fmt.Println("Falling back to user configuration...")
				configureUserPath(homeDir, installPath)
			} else {
				// 如果文件存在，检查是否已包含 Go 的 PATH
				content, readErr := os.ReadFile(systemGoProfilePath)
				if readErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: Failed to read %s: %v\n", systemGoProfilePath, readErr)
					fmt.Println("Falling back to user configuration...")
					configureUserPath(homeDir, installPath)
				} else {
					if strings.Contains(string(content), goBinPath) {
						fmt.Printf("%s already contains Go bin directory in PATH. Skipping modification.\n", systemGoProfilePath)
						printGlobalActivationInstruction(systemGoProfilePath)
					} else {
						// 如果不存在 Go 的 PATH，则以追加模式打开文件
						file, openErr := os.OpenFile(systemGoProfilePath, os.O_APPEND|os.O_WRONLY, 0644)
						if openErr != nil {
							fmt.Fprintf(os.Stderr, "Warning: Failed to open %s for appending: %v\n", systemGoProfilePath, openErr)
							fmt.Println("Falling back to user configuration...")
							configureUserPath(homeDir, installPath)
						} else {
							defer file.Close()
							_, writeErr := file.WriteString("\n" + exportLine + "\n")
							if writeErr != nil {
								fmt.Fprintf(os.Stderr, "Warning: Failed to write to %s: %v\n", systemGoProfilePath, writeErr)
								fmt.Println("Falling back to user configuration...")
								configureUserPath(homeDir, installPath)
							} else {
								fmt.Printf("Appended '%s' to %s.\n", exportLine, systemGoProfilePath)
								printGlobalActivationInstruction(systemGoProfilePath)
							}
						}
					}
				}
			}
		}
	} else {
		// 未设置 --root flag 或没有 root 权限，执行用户配置
		if rootMode && os.Geteuid() != 0 {
			fmt.Println("Warning: --root flag set, but not running with root privileges. Falling back to user configuration.")
		} else {
			fmt.Println("Configuring PATH for current user...")
		}
		configureUserPath(homeDir, installPath)
	}

	// 最终安装成功提示
	fmt.Println("\nGo environment installation complete")
	fmt.Printf("Installed version: %s\n", versionToInstall)
}

// configureUserPath 配置用户主目录下的 PATH 环境变量
func configureUserPath(homeDir, installPath string) {
	profilePath := filepath.Join(homeDir, ".profile")
	goBinPath := filepath.Join(installPath, "bin")
	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", goBinPath)

	fmt.Printf("Attempting to add Go bin directory to %s...\n", profilePath)

	// 检查 .profile 文件是否存在
	_, err := os.Stat(profilePath)
	if os.IsNotExist(err) {
		// 如果 .profile 不存在，创建并写入 PATH 行
		fmt.Printf(".profile not found, creating %s...\n", profilePath)
		file, createErr := os.Create(profilePath)
		if createErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create %s: %v\n", profilePath, createErr)
			fmt.Println("Please manually add Go's bin directory to your PATH")
			printManualPathInstruction(installPath)
		} else {
			defer file.Close()
			_, writeErr := file.WriteString(exportLine + "\n")
			if writeErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to write to %s: %v\n", profilePath, writeErr)
				fmt.Println("Please manually add Go's bin directory to your PATH")
				printManualPathInstruction(installPath)
			} else {
				fmt.Printf("Added '%s' to %s.\n", exportLine, profilePath)
				printUserActivationInstruction(profilePath)
			}
		}
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to check %s: %v\n", profilePath, err)
		fmt.Println("Please manually add Go's bin directory to your PATH")
		printManualPathInstruction(installPath)
	} else {
		// 如果 .profile 存在，读取文件内容，检查是否已包含 Go 的 PATH
		content, readErr := os.ReadFile(profilePath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to read %s: %v\n", profilePath, readErr)
			fmt.Println("Please manually add Go's bin directory to your PATH")
			printManualPathInstruction(installPath)
		} else {
			if strings.Contains(string(content), goBinPath) {
				fmt.Printf("%s already contains Go bin directory in PATH. Skipping modification.\n", profilePath)
				printUserActivationInstruction(profilePath)
			} else {
				// 如果不存在 Go 的 PATH，则以追加模式打开文件
				file, openErr := os.OpenFile(profilePath, os.O_APPEND|os.O_WRONLY, 0644)
				if openErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: Failed to open %s for appending: %v\n", profilePath, openErr)
					fmt.Println("Please manually add Go's bin directory to your PATH")
					printManualPathInstruction(installPath)
				} else {
					defer file.Close()
					_, writeErr := file.WriteString("\n" + exportLine + "\n")
					if writeErr != nil {
						fmt.Fprintf(os.Stderr, "Warning: Failed to write to %s: %v\n", profilePath, writeErr)
						fmt.Println("Please manually add Go's bin directory to your PATH")
						printManualPathInstruction(installPath)
					} else {
						fmt.Printf("Appended '%s' to %s.\n", exportLine, profilePath)
						printUserActivationInstruction(profilePath)
					}
				}
			}
		}
	}
}

// printManualPathInstruction 打印手动设置 PATH 的说明
func printManualPathInstruction(installPath string) {
	fmt.Println("\nManual step required:")
	fmt.Println("Please add Go's bin directory to your PATH environment variable")
	fmt.Println("You can add the following line to your shell configuration file (e.g., ~/.bashrc, ~/.zshrc, ~/.profile):")
	fmt.Printf("\nexport PATH=\"%s/bin:$PATH\"\n\n", installPath)
	fmt.Println("After adding the line, please run the following command to apply the changes:")
	fmt.Printf("source ~/.bashrc  (or your shell configuration file)\n")
}

// printUserActivationInstruction 打印用户 PATH 配置的激活说明
func printUserActivationInstruction(profilePath string) {
	fmt.Println("\nTo activate the changes for your user, please either:")
	fmt.Println("1. Log out and log back in")
	fmt.Printf("2. Run: source %s\n", profilePath)
	fmt.Println("\nAfter that, you can run 'go version' to verify the installation")
}

// printGlobalActivationInstruction 打印全局 PATH 配置的激活说明
func printGlobalActivationInstruction(profilePath string) {
	fmt.Println("\nTo activate the global changes, please either:")
	fmt.Println("1. Log out and log back in (for all users)")
	fmt.Printf("2. Run: source %s\n", profilePath)
	fmt.Println("\nAfter that, you can open a new terminal or run 'go version' to verify the installation")
}

/*
// getSystemInfo 检测系统内核版本和架构
// 仅在 Linux 系统上使用 syscall.Uname
func getSystemInfo() (kernelVersion, architecture string, err error) {
	if runtime.GOOS != "linux" {
		debugPrint("syscall.Uname is only available on Linux. Current OS: %s. Using runtime.GOARCH.", runtime.GOOS)
		return "", runtime.GOARCH, fmt.Errorf("syscall.Uname is only available on Linux, using runtime.GOARCH")
	}

	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		debugPrint("Failed to get system info using Uname: %v. Using runtime.GOARCH.", err)
		return "", runtime.GOARCH, fmt.Errorf("failed to get system info using Uname, using runtime.GOARCH: %w", err)
	}

	kernelVersion = strings.Trim(string(bytesToString(uname.Release[:])), "\x00")
	architecture = strings.Trim(string(bytesToString(uname.Machine[:])), "\x00")

	debugPrint("Uname Release: %s, Machine: %s", kernelVersion, architecture)

	return kernelVersion, architecture, nil
}
*/

// mapArchitecture 将检测到的系统架构映射到 Go 的 GOARCH 值
func mapArchitecture(detectedArch string) string {
	lowerArch := strings.ToLower(detectedArch)
	var goArch string

	switch lowerArch {
	case "x86_64", "amd64":
		goArch = "amd64"
	case "aarch64", "arm64":
		goArch = "arm64"
	case "i386", "i686":
		goArch = "386"
	case "armv6l", "armv7l":
		goArch = "arm"
	case "ppc64le":
		goArch = "ppc64le"
	case "s390x":
		goArch = "s390x"
	default:
		goArch = detectedArch
		debugPrint("No specific mapping for detected architecture '%s', using as is", detectedArch)
	}
	debugPrint("Mapped detected architecture '%s' to Go architecture '%s'", detectedArch, goArch)
	return goArch
}

// bytesToString 将 []int8 转换为 []byte，去除末尾的 \x00 字符
func bytesToString(bs []int8) []byte {
	b := make([]byte, 0, len(bs))
	for _, v := range bs {
		if v == 0 {
			break
		}
		b = append(b, byte(v))
	}
	return b
}

// getAllGoVersions 获取所有 Go 版本信息列表 (从 go.dev/dl/?mode=json JSON API)
func getAllGoVersions() ([]GoVersionInfo, error) {
	resp, err := http.Get(goVersionURL)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch version info from %s: %w", goVersionURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch version info from %s, status code: %d", goVersionURL, resp.StatusCode)
	}

	var versions []GoVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("failed to parse version info from %s: %w", goVersionURL, err)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no Go version info found in %s", goVersionURL)
	}

	return versions, nil
}

// getLatestGoVersionFromTextHTTP 从 go.dev/VERSION?m=text 获取最新版本号 (使用 net/http)
func getLatestGoVersionFromTextHTTP() (string, error) {
	resp, err := http.Get(latestVersionTextURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest version from %s via HTTP: %w", latestVersionTextURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch latest version from %s via HTTP, status code: %d", latestVersionTextURL, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body from %s: %w", latestVersionTextURL, err)
	}

	lines := strings.Split(strings.TrimSpace(string(bodyBytes)), "\n")
	if len(lines) < 1 {
		return "", fmt.Errorf("unexpected output format from %s via HTTP", latestVersionTextURL)
	}

	versionLine := lines[0]
	if !strings.HasPrefix(versionLine, "go") {
		return "", fmt.Errorf("unexpected version format in output from %s via HTTP: %s", latestVersionTextURL, versionLine)
	}

	version := strings.TrimPrefix(versionLine, "go")
	debugPrint("Got latest version from text URL via HTTP: %s", version)
	return version, nil
}

// downloadFile 下载文件并显示进度条
func downloadFile(url, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed, status code: %d", resp.StatusCode)
	}

	contentLength := resp.ContentLength
	if contentLength <= 0 {
		fmt.Println("Warning: Cannot get content length for progress bar")
	}

	progressBar := &progressBarWriter{Total: contentLength, downloaded: 0, start: time.Now()}
	reader := io.TeeReader(resp.Body, progressBar)

	_, err = io.Copy(out, reader)
	fmt.Println()
	return err
}

// progressBarWriter 提供下载进度反馈，实现 io.Writer 接口
type progressBarWriter struct {
	Total      int64
	downloaded int64
	start      time.Time
	lastPrint  time.Time
}

// Write io.Writer 接口方法
func (pb *progressBarWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	pb.downloaded += int64(n)

	if time.Since(pb.lastPrint) > 100*time.Millisecond || pb.downloaded == pb.Total {
		pb.printProgress()
		pb.lastPrint = time.Now()
	}

	return n, nil
}

// printProgress 打印当前下载进度信息
func (pb *progressBarWriter) printProgress() {
	if pb.Total <= 0 {
		fmt.Printf("\rDownloaded: %s", formatBytes(pb.downloaded))
	} else {
		percentage := float64(pb.downloaded) / float64(pb.Total) * 100
		elapsed := time.Since(pb.start)
		speed := float64(pb.downloaded) / elapsed.Seconds()

		fmt.Printf("\rDownloading: %.2f%% (%s / %s) Speed: %s/s Elapsed: %s",
			percentage,
			formatBytes(pb.downloaded),
			formatBytes(pb.Total),
			formatBytes(int64(speed)),
			elapsed.Truncate(time.Second),
		)
	}
}

// formatBytes 将字节数格式化为人类可读的字符串
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// extractTarGz 解压 tar.gz 文件到指定目录
func extractTarGz(filePath, destDir string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)

		// 安全检查：确保解压路径在目标目录内
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", target)
		}

		switch header.Typeflag {
		case tar.TypeDir: // 目录
			if _, err := os.Stat(target); os.IsNotExist(err) {
				if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				debugPrint("Directory %s already exists, setting mode to %v", target, os.FileMode(header.Mode))
				if err := os.Chmod(target, os.FileMode(header.Mode)); err != nil {
					return err
				}
			}
		case tar.TypeReg: // 普通文件
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
		}
	}
	return nil
}
