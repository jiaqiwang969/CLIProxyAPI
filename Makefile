.PHONY: install-menubar open-menubar smoke flake-show build-package

install-menubar:
	./scripts/install-menubar.sh

open-menubar:
	open "$(HOME)/Applications/CLIProxyMenuBar.app"

smoke:
	bash ./scripts/smoke.sh

flake-show:
	nix flake show

build-package:
	nix build .#packages.aarch64-darwin.default
