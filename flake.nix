{
  description = "CLIProxyAPI Darwin flake for the Go backend and Swift menubar integration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-25.11?shallow=1";
  };

  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      darwinSystems = [ "aarch64-darwin" "x86_64-darwin" ];
      forDarwinSystems = f:
        lib.genAttrs darwinSystems (system: f (import nixpkgs { inherit system; }));

      mkCLIProxyPackage = pkgs:
        (pkgs.buildGoModule.override { go = pkgs.go_1_26; }) rec {
          pname = "cliproxy-api";
          version = "0.1.0";
          src = ./.;
          modRoot = "apps/server-go";
          subPackages = [ "./cmd/server" ];
          vendorHash = "sha256-jlUmO7qVlK5kvaw7q3e7Tk2shPH8XlGvZ73GlNrHoJI=";
          doCheck = false;

          ldflags = [
            "-s"
            "-w"
            "-X main.Version=${version}"
            "-X main.Commit=${self.rev or "dirty"}"
            "-X main.BuildDate=${self.lastModifiedDate or "unknown"}"
          ];

          postInstall = ''
            if [ -f "$out/bin/server" ]; then
              mv "$out/bin/server" "$out/bin/cli-proxy-api"
            fi

            install -Dm644 config.example.yaml \
              "$out/share/cliproxy-api/config.example.yaml"

            cat > "$out/bin/cliproxy-auggie-login" <<'EOF'
#!/bin/sh
set -eu
BIN_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
CONFIG_PATH="''${CLIPROXY_CONFIG_PATH:-''${HOME}/.cliproxyapi/config.yaml}"
exec "$BIN_DIR/cli-proxy-api" -config "$CONFIG_PATH" -auggie-login "$@"
EOF
            chmod 755 "$out/bin/cliproxy-auggie-login"

            cat > "$out/bin/cliproxy-antigravity-login" <<'EOF'
#!/bin/sh
set -eu
BIN_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
CONFIG_PATH="''${CLIPROXY_CONFIG_PATH:-''${HOME}/.cliproxyapi/config.yaml}"
exec "$BIN_DIR/cli-proxy-api" -config "$CONFIG_PATH" -antigravity-login "$@"
EOF
            chmod 755 "$out/bin/cliproxy-antigravity-login"
          '';

          meta = with pkgs.lib; {
            description = "CLIProxyAPI backend with managed Auggie and Antigravity login helpers";
            license = licenses.mit;
            platforms = darwinSystems;
          };
        };
    in
    {
      packages = forDarwinSystems (pkgs:
        let
          cliproxyPackage = mkCLIProxyPackage pkgs;
        in
        {
          default = cliproxyPackage;
          cliproxy-api = cliproxyPackage;
        });

      darwinModules.default = import ./nix/modules/cliproxy-api-darwin.nix self;
    };
}
