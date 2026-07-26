# Release-synchronized source copy. The published formula lives at:
# https://github.com/vibe-agi/homebrew-tap/blob/main/Formula/hideout.rb
class Hideout < Formula
  desc "Run AI agents and untrusted CLIs in a local VM"
  homepage "https://github.com/vibe-agi/hideout"
  url "https://github.com/vibe-agi/hideout/releases/download/v0.1.0-alpha.1/hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz"
  sha256 "9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4"
  license "Apache-2.0"

  depends_on arch: :arm64
  depends_on "lima"
  depends_on :macos

  # Homebrew's macOS cleaner recognizes Mach-O and scripts, but these ELF
  # helpers must retain their execute bits so Hideout can copy them into Lima.
  skip_clean "bin/hideout-dns-stub-linux-arm64",
             "bin/hideout-hostfsd-linux-arm64",
             "bin/hideout-session-supervisor-linux-arm64",
             "bin/hideout-workspace-portal-linux-arm64",
             "bin/hideout-shim-linux-arm64",
             "bin/tun2socks-linux-arm64"

  def install
    package_root = buildpath
    install_store = prefix/".homebrew-install-store"

    odie "Hideout package manifest is missing" unless (package_root/"package-manifest.json").file?
    odie "Hideout package installer is missing" unless (package_root/"install.sh").executable?

    system "/usr/bin/codesign", "--verify", "--strict", package_root/"bin/hideout"
    system package_root/"install.sh",
           "--prefix", prefix,
           "--store", install_store,
           "--skip-init"
  end

  def caveats
    <<~EOS
      Hideout is installed but not configured. Review and create the supported
      default configuration with:

        hideout setup
        hideout doctor
        cd /path/to/project
        hideout run -- git status --short

      Setup does not start a VM or download the runtime. First run downloads
      the retained developer runtime separately; expect
      approximately 1 GB. Hideout user state remains under ~/.hideout and is
      preserved by brew upgrade and brew uninstall. To inspect or remove that
      durable state separately, run:

        hideout help uninstall
    EOS
  end

  test do
    assert_match "hideout #{version}", shell_output("#{bin}/hideout version")
    system bin/"hideout", "package", "verify", prefix
    refute_path_exists testpath/".hideout"
  end
end
