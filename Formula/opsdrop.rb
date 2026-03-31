class Opsdrop < Formula
  desc "CLI for sharing files and clipboard snippets across devices via OpsDrop"
  homepage "https://github.com/hemp0r/opsdrop"
  version "${VERSION}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/hemp0r/opsdrop/releases/download/v#{version}/opsdrop-darwin-arm64"
      sha256 "${SHA256_DARWIN_ARM64}"
    else
      url "https://github.com/hemp0r/opsdrop/releases/download/v#{version}/opsdrop-darwin-amd64"
      sha256 "${SHA256_DARWIN_AMD64}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/hemp0r/opsdrop/releases/download/v#{version}/opsdrop-linux-arm64"
      sha256 "${SHA256_LINUX_ARM64}"
    else
      url "https://github.com/hemp0r/opsdrop/releases/download/v#{version}/opsdrop-linux-amd64"
      sha256 "${SHA256_LINUX_AMD64}"
    end
  end

  def install
    binary = Dir.glob("opsdrop-*").first
    bin.install binary => "opsdrop"
  end

  test do
    assert_match "opsdrop version", shell_output("#{bin}/opsdrop --version")
  end
end
