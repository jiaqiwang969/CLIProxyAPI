self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.cliproxy-api;
  homeDir = "/Users/${cfg.user}";
  runtimeDir =
    if cfg.workDir == "" then
      "${homeDir}/.cliproxyapi"
    else
      cfg.workDir;
  authDir =
    if cfg.authDir == "" then
      "${runtimeDir}/auth"
    else
      cfg.authDir;
  logDir = "${homeDir}/Library/Logs/CLIProxyAPI";
  configFile = "${runtimeDir}/config.yaml";
  yamlFormat = pkgs.formats.yaml { };
  generatedConfig = yamlFormat.generate "cliproxy-config.yaml" {
    host = cfg.host;
    port = cfg.port;
    "remote-management" = {
      "allow-remote" = false;
      "secret-key" = cfg.managementKey;
      "disable-control-panel" = cfg.disableControlPanel;
    };
    "auth-dir" = authDir;
    "api-keys" = cfg.apiKeys;
    "usage-statistics-enabled" = cfg.usageStatisticsEnabled;
  };
in
{
  options.services.cliproxy-api = {
    enable = lib.mkEnableOption "CLIProxyAPI local backend service";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      description = "The CLIProxyAPI backend package to run.";
    };

    user = lib.mkOption {
      type = lib.types.str;
      description = "The macOS user who owns the CLIProxy runtime directory.";
    };

    host = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1";
      description = "Host interface for the CLIProxy backend.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 8317;
      description = "Port for the CLIProxy backend.";
    };

    managementKey = lib.mkOption {
      type = lib.types.str;
      default = "cliproxy-menubar-dev";
      description = "Management API key written into the generated config file.";
    };

    workDir = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "/Users/me/.cliproxyapi";
      description = "Runtime directory containing config.yaml and the backend binary symlink.";
    };

    authDir = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "/Users/me/.cliproxyapi/auth";
      description = "Directory used to store OAuth auth files.";
    };

    apiKeys = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "sk-local-dev" ];
      description = "Top-level client API keys written into the generated config file.";
    };

    disableControlPanel = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Disable the built-in management panel asset sync and route.";
    };

    usageStatisticsEnabled = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Enable usage statistics for the menubar dashboard.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ];

    launchd.user.agents.cliproxy-api = {
      serviceConfig = {
        Label = "com.jiaqi.cliproxy-api";
        ProgramArguments = [
          "${cfg.package}/bin/cli-proxy-api"
          "-config"
          configFile
        ];
        RunAtLoad = true;
        ProcessType = "Background";
        WorkingDirectory = runtimeDir;
        StandardOutPath = "${logDir}/stdout.log";
        StandardErrorPath = "${logDir}/stderr.log";
        EnvironmentVariables = {
          HOME = homeDir;
        };
      };
    };

    system.activationScripts.postActivation.text = ''
      CLIPROXY_RUNTIME_DIR="${runtimeDir}"
      CLIPROXY_AUTH_DIR="${authDir}"
      CLIPROXY_LOG_DIR="${logDir}"
      CLIPROXY_CONFIG_FILE="${configFile}"
      CLIPROXY_SOURCE_CONFIG="${generatedConfig}"
      CLIPROXY_PACKAGE_BIN="${cfg.package}/bin/cli-proxy-api"

      install -d -m 700 -o ${cfg.user} -g staff \
        "$CLIPROXY_RUNTIME_DIR" \
        "$CLIPROXY_AUTH_DIR"
      install -d -m 755 -o ${cfg.user} -g staff "$CLIPROXY_LOG_DIR"

      if [ ! -f "$CLIPROXY_CONFIG_FILE" ] || ! cmp -s "$CLIPROXY_SOURCE_CONFIG" "$CLIPROXY_CONFIG_FILE"; then
        cp "$CLIPROXY_SOURCE_CONFIG" "$CLIPROXY_CONFIG_FILE"
        chown ${cfg.user}:staff "$CLIPROXY_CONFIG_FILE"
        chmod 600 "$CLIPROXY_CONFIG_FILE"
      fi

      ln -sfn "$CLIPROXY_PACKAGE_BIN" "$CLIPROXY_RUNTIME_DIR/cli-proxy-api"
    '';
  };
}
