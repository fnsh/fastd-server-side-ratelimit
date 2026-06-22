{
  description = "fastd server-side ratelimit service and NixOS module";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      mkPackages = system:
        let
          pkgs = import nixpkgs {
            inherit system;
          };

          applyShaperBody = lib.strings.replaceStrings [
            "#!/bin/sh\n"
          ] [
            ""
          ] (builtins.readFile ./contrib/apply-shaper.sh);

          applyShaperPackage = pkgs.writeShellApplication {
            name = "fssrl-apply-shaper";
            runtimeInputs = [ pkgs.iproute2 ];
            text = applyShaperBody;
          };
        in
        rec {
          apply-shaper = applyShaperPackage;

          server = pkgs.buildGoModule {
            pname = "fastd-server-side-ratelimit";
            version = "unstable";
            src = ./server;

            vendorHash = "sha256-OMyIAUkFlZVZtvW4c5ZJ6XQCDPooAc0DfeSZIExelLg=";
            subPackages = [ "./cmd" ];
            doCheck = false;

            postInstall = ''
              mv $out/bin/cmd $out/bin/fssrl-server
            '';

            meta = with pkgs.lib; {
              description = "Server-side rate limiter for fastd";
              homepage = "https://github.com/freifunk/fastd-server-side-ratelimit";
              license = licenses.mit;
              mainProgram = "fssrl-server";
            };
          };

          default = server;
        };

      mkModule = { config, lib, pkgs, ... }:
        let
          cfg = config.services.fastd-server-side-ratelimit;
          yaml = pkgs.formats.yaml { };

          generatedConfig = yaml.generate "fastd-server-side-ratelimit.yaml" (
            {
              bandwith.min.download = cfg.minDownload;
              bandwith.min.upload = cfg.minUpload;
              shaper_script = cfg.shaperScript;
            }
            // lib.optionalAttrs (cfg.maxDownload != null || cfg.maxUpload != null) {
              bandwith.max = lib.optionalAttrs (cfg.maxDownload != null) {
                download = cfg.maxDownload;
              } // lib.optionalAttrs (cfg.maxUpload != null) {
                upload = cfg.maxUpload;
              };
            }
            // lib.optionalAttrs (cfg.interfacePrefix != "") {
              interface_prefix = cfg.interfacePrefix;
            }
            // lib.optionalAttrs (cfg.interfaceSuffix != "") {
              interface_suffix = cfg.interfaceSuffix;
            }
          );

          defaultPackage = self.packages.${pkgs.system}.server;
        in
        {
          options.services.fastd-server-side-ratelimit = {
            enable = lib.mkEnableOption "fastd server-side ratelimit server";

            package = lib.mkOption {
              type = lib.types.nullOr lib.types.package;
              default = defaultPackage;
              defaultText = lib.literalExpression ''
                if pkgs.system == "x86_64-linux" then self.packages.${pkgs.system}.server else null
              '';
              description = "Package providing the server binary.";
            };

            minDownload = lib.mkOption {
              type = lib.types.ints.unsigned;
              default = 12000;
              description = "Minimum downstream rate in kbit/s.";
            };

            minUpload = lib.mkOption {
              type = lib.types.ints.unsigned;
              default = 6000;
              description = "Minimum upstream rate in kbit/s.";
            };

            maxDownload = lib.mkOption {
              type = with lib.types; nullOr ints.unsigned;
              default = null;
              description = "Optional maximum downstream rate in kbit/s.";
            };

            maxUpload = lib.mkOption {
              type = with lib.types; nullOr ints.unsigned;
              default = null;
              description = "Optional maximum upstream rate in kbit/s.";
            };

            interfacePrefix = lib.mkOption {
              type = lib.types.str;
              default = "";
              description = "Only handle interfaces with this name prefix.";
            };

            interfaceSuffix = lib.mkOption {
              type = lib.types.str;
              default = "";
              description = "Only handle interfaces with this name suffix.";
            };

            shaperScript = lib.mkOption {
              type = lib.types.str;
              default = self.packages.${pkgs.system}.apply-shaper + "/bin/fssrl-apply-shaper";
              description = "Executable used to apply the shaper settings.";
            };
          };

          config = lib.mkIf cfg.enable {
            assertions = [
              {
                assertion = cfg.package != null;
                message = "services.fastd-server-side-ratelimit.package must be set on this system";
              }
            ];

            systemd.services.fastd-server-side-ratelimit = {
              description = "fastd server-side ratelimit";
              wantedBy = [ "multi-user.target" ];
              after = [ "network-online.target" ];
              wants = [ "network-online.target" ];

              serviceConfig = {
                ExecStart = lib.escapeShellArgs [
                  "${cfg.package}/bin/fssrl-server"
                  "-config"
                  generatedConfig
                ];
                Restart = "always";
                RestartSec = 5;
                User = "root";
              };

              path = [ pkgs.iproute2 ];
            };
          };
        };
    in
    {
      packages = {
        x86_64-linux = mkPackages "x86_64-linux";
        aarch64-linux = mkPackages "aarch64-linux";
      };

      nixosModules.default = mkModule;
    };
}