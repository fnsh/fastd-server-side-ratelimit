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

          mkTargetLimit = limit:
            {
              target = limit.target;
            }
            // lib.optionalAttrs (limit.subtarget != null) {
              subtarget = limit.subtarget;
            }
            // lib.optionalAttrs (limit.minDownstreamRate != null) {
              min_downstream_rate = limit.minDownstreamRate;
            }
            // lib.optionalAttrs (limit.maxDownstreamRate != null) {
              max_downstream_rate = limit.maxDownstreamRate;
            }
            // lib.optionalAttrs (limit.minUpstreamRate != null) {
              min_upstream_rate = limit.minUpstreamRate;
            }
            // lib.optionalAttrs (limit.maxUpstreamRate != null) {
              max_upstream_rate = limit.maxUpstreamRate;
            }
            // lib.optionalAttrs (limit.initialDownstreamRate != null) {
              initial_downstream_rate = limit.initialDownstreamRate;
            }
            // lib.optionalAttrs (limit.initialUpstreamRate != null) {
              initial_upstream_rate = limit.initialUpstreamRate;
            };

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
            // lib.optionalAttrs (cfg.targetLimits != [ ]) {
              target_limits = map mkTargetLimit cfg.targetLimits;
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

            targetLimits = lib.mkOption {
              type = with lib.types; listOf (submodule {
                options = {
                  target = lib.mkOption {
                    type = str;
                    default = "";
                    description = "OpenWrt target (empty string is the default fallback target).";
                  };

                  subtarget = lib.mkOption {
                    type = nullOr str;
                    default = null;
                    description = "Optional OpenWrt subtarget.";
                  };

                  minDownstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional minimum downstream rate in kbit/s for this target limit.";
                  };

                  maxDownstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional maximum downstream rate in kbit/s for this target limit.";
                  };

                  minUpstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional minimum upstream rate in kbit/s for this target limit.";
                  };

                  maxUpstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional maximum upstream rate in kbit/s for this target limit.";
                  };

                  initialDownstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional initial downstream rate in kbit/s for this target limit.";
                  };

                  initialUpstreamRate = lib.mkOption {
                    type = nullOr ints.unsigned;
                    default = null;
                    description = "Optional initial upstream rate in kbit/s for this target limit.";
                  };
                };
              });
              default = [ ];
              description = "Per-target and per-subtarget limits written as target_limits in the server config.";
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