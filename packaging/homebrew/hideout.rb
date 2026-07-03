class Hideout < Formula
  desc "Local privacy runner for AI agents and developer tools"
  homepage "https://github.com/vibe-agi/hideout"
  head "https://github.com/vibe-agi/hideout.git", branch: "master"
  license :cannot_represent

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-o", bin/"hideout", "./cmd/hideout"
    system "go", "build", "-trimpath", "-o", bin/"hideout-shim", "./cmd/hideout-shim"

    linux_arch = Hardware::CPU.arm? ? "arm64" : "amd64"
    system bin/"hideout", "shim", "build-linux",
      "--out", bin/"hideout-shim-linux-#{linux_arch}",
      "--goarch", linux_arch,
      "--source", buildpath
    system bin/"hideout", "hostfsd", "build-linux",
      "--out", bin/"hideout-hostfsd-linux-#{linux_arch}",
      "--goarch", linux_arch,
      "--source", buildpath
  end

  test do
    store = testpath/".hideout"
    workspace = testpath/"workspace"
    workspace.mkpath

    ENV["HIDEOUT_STORE_ROOT"] = store
    system bin/"hideout", "init", "--no-input", "--backend", "native", "--network", "direct"
    system bin/"hideout", "doctor", "--backend", "native", "--workspace", workspace

    assert_path_exists store/"install-state.json"
    assert_path_exists store/"logs/init-audit.jsonl"
    assert_path_exists store/"profiles/default/profile.json"
  end
end
