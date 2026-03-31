#!/bin/sh
set -e

# OpsDrop CLI installer
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Hemp0r/opsdrop/main/install.sh | sh
#   wget -qO- https://raw.githubusercontent.com/Hemp0r/opsdrop/main/install.sh | sh

REPO="Hemp0r/opsdrop"
BINARY="opsdrop"

# --- helper functions ---

command_exists() {
	command -v "$1" > /dev/null 2>&1
}

fmt_error() {
	printf '\033[31mError: %s\033[0m\n' "$1" >&2
}

fmt_info() {
	printf '\033[34m%s\033[0m\n' "$1"
}

# --- detect OS ---

detect_os() {
	os="$(uname -s)"
	case "$os" in
		Linux)   echo "linux" ;;
		Darwin)  echo "darwin" ;;
		*)       fmt_error "Unsupported operating system: $os"; exit 1 ;;
	esac
}

# --- detect architecture ---

detect_arch() {
	arch="$(uname -m)"
	case "$arch" in
		x86_64|amd64)   echo "amd64" ;;
		aarch64|arm64)   echo "arm64" ;;
		*)               fmt_error "Unsupported architecture: $arch"; exit 1 ;;
	esac
}

# --- determine install directory ---

install_dir() {
	if [ "$(detect_os)" = "darwin" ]; then
		echo "/usr/local/bin"
	else
		echo "/usr/local/bin"
	fi
}

# --- resolve latest version tag ---

get_latest_version() {
	if command_exists curl; then
		curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" | grep -oE '[^/]+$'
	elif command_exists wget; then
		wget -qS -O /dev/null "https://github.com/${REPO}/releases/latest" 2>&1 | grep -i 'Location:' | grep -oE '[^/]+$' | tr -d '\r'
	else
		fmt_error "curl or wget is required"
		exit 1
	fi
}

# --- download helper ---

download() {
	url="$1"
	dest="$2"
	if command_exists curl; then
		curl -fsSL -o "$dest" "$url"
	elif command_exists wget; then
		wget -qO "$dest" "$url"
	else
		fmt_error "curl or wget is required"
		exit 1
	fi
}

# --- verify checksum ---

verify_checksum() {
	binary_path="$1"
	asset_name="$2"
	checksum_file="$3"

	if [ ! -f "$checksum_file" ]; then
		fmt_info "Skipping checksum verification (checksums not available)"
		return 0
	fi

	expected="$(grep " ${asset_name}\$" "$checksum_file" | awk '{print $1}')"
	if [ -z "$expected" ]; then
		# try without leading space (some formats differ)
		expected="$(grep "${asset_name}" "$checksum_file" | awk '{print $1}')"
	fi

	if [ -z "$expected" ]; then
		fmt_info "Skipping checksum verification (asset not found in checksums)"
		return 0
	fi

	if command_exists sha256sum; then
		actual="$(sha256sum "$binary_path" | awk '{print $1}')"
	elif command_exists shasum; then
		actual="$(shasum -a 256 "$binary_path" | awk '{print $1}')"
	else
		fmt_info "Skipping checksum verification (sha256sum/shasum not found)"
		return 0
	fi

	if [ "$expected" != "$actual" ]; then
		fmt_error "Checksum verification failed!"
		fmt_error "  Expected: $expected"
		fmt_error "  Actual:   $actual"
		exit 1
	fi

	fmt_info "Checksum verified."
}

# --- run command with sudo if needed ---

maybe_sudo() {
	if [ "$(id -u)" -ne 0 ]; then
		if command_exists sudo; then
			sudo "$@"
		elif command_exists doas; then
			doas "$@"
		else
			fmt_error "This script requires root privileges. Please run as root or install sudo."
			exit 1
		fi
	else
		"$@"
	fi
}

# --- main ---

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"

	fmt_info "Detecting system... ${os}/${arch}"

	fmt_info "Fetching latest release version..."
	version="$(get_latest_version)"
	if [ -z "$version" ]; then
		fmt_error "Could not determine latest version"
		exit 1
	fi
	fmt_info "Latest version: ${version}"

	asset_name="${BINARY}-${os}-${arch}"
	download_url="https://github.com/${REPO}/releases/download/${version}/${asset_name}"
	checksums_url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"

	tmpdir="$(mktemp -d)"
	trap 'rm -rf "$tmpdir"' EXIT

	fmt_info "Downloading ${asset_name}..."
	download "$download_url" "${tmpdir}/${asset_name}"

	fmt_info "Downloading checksums..."
	download "$checksums_url" "${tmpdir}/checksums.txt" 2>/dev/null || true

	verify_checksum "${tmpdir}/${asset_name}" "$asset_name" "${tmpdir}/checksums.txt"

	dest="$(install_dir)"
	fmt_info "Installing ${BINARY} to ${dest}/${BINARY}..."
	chmod +x "${tmpdir}/${asset_name}"
	maybe_sudo mkdir -p "$dest"
	maybe_sudo mv "${tmpdir}/${asset_name}" "${dest}/${BINARY}"

	fmt_info "Successfully installed ${BINARY} ${version} to ${dest}/${BINARY}"
	fmt_info ""
	fmt_info "Run '${BINARY} --help' to get started."
}

main
