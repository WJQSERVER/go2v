#! /bin/bash

# 函数：安装软件包
install() {
    # 检查是否为 root 用户，非 root 用户在 Linux 和 FreeBSD 上不执行包管理器安装
    if [ "$OS" != "Darwin" ] && [ "$EUID" -ne 0 ]; then
        echo "Running in rootless mode, skipping system package installation."
        echo "Please ensure curl, wget, and sed are installed manually if needed."
        return 0
    fi

    if [ $# -eq 0 ]; then
        echo "Error: No packages specified"
        return 1
    fi

    for package in "$@"; do
        # 检查软件包是否已安装
        if ! command -v "$package" &>/dev/null; then
            echo "Installing dependency: $package"
            if command -v dnf &>/dev/null; then
                dnf -y update && dnf install -y "$package"
            elif command -v yum &>/dev/null; then
                yum -y update && yum -y install "$package"
            elif command -v apt &>/dev/null; then
                apt update -y && apt install -y "$package"
            elif command -v apk &>/dev/null; then
                apk update && apk add "$package"
            elif command -v brew &>/dev/null; then
                brew install "$package"
            elif command -v pkg &>/dev/null; then
                pkg update && pkg install -y "$package"
            else
                echo "Error: Unknown package manager. Please install $package manually"
                # 在 rootless 模式下不退出，只提示
                if [ "$EUID" -ne 0 ] && [ "$OS" != "Darwin" ]; then
                     echo "Running in rootless mode, cannot install $package. Please install manually."
                else
                    return 1
                fi
            fi
        fi
    done

    return 0
}

# 检测系统类型和架构
OS=$(uname)
ARCH=$(uname -m)

# 支持的系统列表
supported_os=("Linux" "Darwin" "FreeBSD")
is_supported=false
for so in "${supported_os[@]}"; do
    if [ "$OS" == "$so" ]; then
        is_supported=true
        break
    fi
done

if [ "$is_supported" == false ]; then
    echo "Error: This script does not support $OS"
    exit 1
fi

# 检测是否为 root 用户
if [ "$EUID" -eq 0 ]; then
    IS_ROOT=true
    echo "Running as root."
else
    IS_ROOT=false
    echo "Running as non-root user. Using rootless mode."
fi

# 在 Linux 和 FreeBSD 上，如果是非 root 用户，不强制要求 root 权限，进入 rootless 模式
if [ "$OS" != "Darwin" ] && [ "$EUID" -ne 0 ]; then
    echo "Proceeding with rootless installation."
else
    # 如果是 root 用户或 macOS，继续检查 macOS 是否需要 root
    if [ "$OS" != "Darwin" ] && [ "$EUID" -ne 0 ]; then
         echo "Error: Please run this script as root (Linux and FreeBSD only)"
         exit 1
    fi
fi


# 安装依赖包
# 在 rootless 模式下，跳过包管理器安装，只提示用户手动安装
if [ "$IS_ROOT" == true ] || [ "$OS" == "Darwin" ]; then
    install curl wget sed
else
    echo "Running in rootless mode, assuming curl, wget, and sed are available or will be installed manually."
fi


# 映射架构名称到 Go 的命名规范
case "$ARCH" in
    x86_64 | amd64 | x86-64)
        GO_ARCH="amd64"
        ;;
    aarch64 | arm64 | armv8 | armv8l)
        GO_ARCH="arm64"
        ;;
    *)
        echo "Error: Unsupported architecture ($ARCH) on $OS"
        exit 1
        ;;
esac

# 根据系统类型和是否为 root 用户确定安装目录
if [ "$IS_ROOT" == true ] && [ "$OS" != "Darwin" ]; then
    # Root 用户在 Linux 和 FreeBSD 上的默认安装路径
    case "$OS" in
        Linux)
            bin_dir="/usr/local/bin/go2v"
            ;;
        FreeBSD)
            bin_dir="/usr/local/bin/go2v"
            ;;
        *)
             # Fallback for unsupported OS as root (shouldn't happen based on earlier check)
            echo "Error: Could not determine root installation directory for $OS"
            exit 1
            ;;
    esac
else
    # 非 root 用户或 macOS 上的安装路径
    bin_dir="$HOME/.local/bin/go2v"
fi


echo "Installation directory set to: $(dirname "${bin_dir}")"

# 创建安装目录
mkdir -p $(dirname "${bin_dir}")

# 获取最新版本号
VERSION_URL="https://raw.githubusercontent.com/WJQSERVER/go2v/main/VERSION"
VERSION=$(curl -s "${VERSION_URL}")

if [ -z "$VERSION" ]; then
    echo "Error: Failed to get latest version from ${VERSION_URL}"
    exit 1
fi

echo "Downloading go2v version: ${VERSION} ($OS/$GO_ARCH)"

# 下载 VERSION 文件到安装目录
wget -O "${bin_dir}.VERSION" "${VERSION_URL}"

# 构建下载链接
DOWNLOAD_URL="https://github.com/WJQSERVER/go2v/releases/download/${VERSION}/go2v-${OS,,}-${GO_ARCH}"

# 下载最新版二进制文件
if ! wget -O "${bin_dir}" "${DOWNLOAD_URL}"; then
    echo "Error: Failed to download file from ${DOWNLOAD_URL}"
    exit 1
fi

# 赋予执行权限
chmod +x "${bin_dir}"

# 输出安装成功信息
echo "go2v installed successfully to ${bin_dir}"
echo "Version: ${VERSION}"

# 提示用户如何添加到 PATH
if [ "$OS" == "Darwin" ] || [ "$IS_ROOT" == false ]; then
    echo "Ensure your \$PATH includes $(dirname "${bin_dir}")"
    echo "You might need to add the following to your ~/.bashrc, ~/.zshrc, or ~/.profile:"
    echo "export PATH=\"$(dirname "${bin_dir}"):\$PATH\""
    echo "Then run 'source ~/.bashrc' (or your shell config) or reopen terminal"
fi

if [ "$OS" == "FreeBSD" ] && [ "$IS_ROOT" == true ]; then
     echo "Ensure your \$PATH includes /usr/local/bin"
fi

if [ "$OS" == "Linux" ] && [ "$IS_ROOT" == true ]; then
     echo "go2v installed to /usr/local/bin, usually included in \$PATH"
fi

echo "You can now run 'go2v'"