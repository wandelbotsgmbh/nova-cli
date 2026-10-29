class Nova < Formula
  desc "wandelbots cli to interact with wandelbots platform"
  homepage "https://github.com/wandelbotsgmbh/nova-cli"
  version "{{ .Version }}"

  on_macos do
    on_intel do
      url "https://github.com/wandelbotsgmbh/nova-cli/releases/download/{{ .Version }}/{{ .ReleaseFilename }}_macos_amd64-{{ .Version }}.tar.gz"
      sha256 "{{ .MacIntelSha256 }}"
    end
    on_arm do
      url "https://github.com/wandelbotsgmbh/nova-cli/releases/download/{{ .Version }}/{{ .ReleaseFilename }}_macos_arm64-{{ .Version }}.tar.gz"
      sha256 "{{ .MacArm64Sha256 }}"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/wandelbotsgmbh/nova-cli/releases/download/{{ .Version }}/{{ .ReleaseFilename }}_linux_amd64-{{ .Version }}.tar.gz"
      sha256 "{{ .LinuxIntelSha256 }}"
    end
    on_arm do
      url "https://github.com/wandelbotsgmbh/nova-cli/releases/download/{{ .Version }}/{{ .ReleaseFilename }}_linux_arm64-{{ .Version }}.tar.gz"
      sha256 "{{ .LinuxArm64Sha256 }}"
    end
  end

  def install
    bin.install "nova"
  end

end