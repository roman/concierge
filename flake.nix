{
  description = "Concierge local task supervision";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/release-25.11";
  inputs.herdr.url = "github:herdrdev/herdr/v0.9.3";

  inputs.treehouse.url = "github:kunchenguid/treehouse/v3.1.1";
  inputs.treehouse.inputs.nixpkgs.follows = "nixpkgs";

  inputs.tincan.url = "github:roman/tincan/92106cf41b5180ded4b151d25fd185f28314e8f2";
  inputs.tincan.inputs.nixpkgs.follows = "nixpkgs";
  inputs.tincan.inputs.herdr.follows = "herdr";

  outputs = { self, nixpkgs, herdr, treehouse, tincan }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forEachSystem = function:
        nixpkgs.lib.genAttrs systems (system: function (import nixpkgs { inherit system; }));
      golinesFlags = "-m 100 --no-reformat-tags --base-formatter=gofmt";
      goSource = pkgs: pkgs.lib.fileset.toSource {
        root = ./.;
        fileset = pkgs.lib.fileset.unions [ ./go.mod ./go.sum ./cmd ./pkg ./internal ./integration ];
      };
      goModule = pkgs: {
        version = "0.1.0";
        src = goSource pkgs;
        go = pkgs.go_1_25;
        vendorHash = "sha256-bbeD3WMgKFYNL38BZY6lDgryg18Ck+f7lJ4N4ZHw9i8=";
      };
      buildCommands = pkgs:
        pkgs.buildGoModule ((goModule pkgs) // {
          pname = "concierge-command";
          subPackages = [ "cmd/concierge" ];
          nativeCheckInputs = [ pkgs.git treehouse.packages.${pkgs.stdenv.hostPlatform.system}.default ];
          TREEHOUSE_TEST_EXECUTABLE = "${treehouse.packages.${pkgs.stdenv.hostPlatform.system}.default}/bin/treehouse";
          TINCAN_TEST_EXECUTABLE = "${tincan.packages.${pkgs.stdenv.hostPlatform.system}.tincan}/bin/tincan";
          checkPhase = ''
            runHook preCheck
            test "$(go list -m)" = github.com/roman/concierge
            transport_version="$(go list -m -f '{{.Version}}' github.com/roman/tincan)"
            case "$transport_version" in
              *-${builtins.substring 0 12 tincan.sourceInfo.rev}) ;;
              *)
                printf 'Transport Go version %s does not match runtime revision %s\n' "$transport_version" '${tincan.sourceInfo.rev}' >&2
                exit 1
                ;;
            esac
            go list -deps ./cmd/concierge > concierge-dependencies
            while IFS= read -r dependency; do
              case "$dependency" in
                github.com/roman/tincan/pkg/inbox|github.com/roman/tincan/pkg/notify) ;;
                github.com/roman/tincan/*)
                  printf 'Forbidden transport dependency: %s\n' "$dependency" >&2
                  exit 1
                  ;;
              esac
            done < concierge-dependencies
            go test ${pkgs.lib.optionalString pkgs.stdenv.isDarwin ''-ldflags "-X=github.com/roman/concierge/pkg/presentation.presentationNamespace=$TMPDIR/presentation-locks"''} ./...
            runHook postCheck
          '';
        });
    in
    {
      packages = forEachSystem (pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;
          herdrPackage = herdr.packages.${system}.default;
          transportPackage = tincan.packages.${system}.tincan;
          supervisorCommand = buildCommands pkgs;
          processTools = if pkgs.stdenv.isLinux then pkgs.procps else
            pkgs.writeShellScriptBin "ps" ''
              exec /bin/ps "$@"
            '';
          conciergeRuntime = pkgs.symlinkJoin {
            name = "concierge-runtime";
            paths = [ herdrPackage processTools pkgs.git transportPackage treehouse.packages.${system}.default ];
          };
          concierge = pkgs.runCommand "concierge-0.1.0" {
            nativeBuildInputs = [ pkgs.makeWrapper ];
          } ''
            install -Dm755 ${supervisorCommand}/bin/concierge "$out/bin/concierge"
            wrapProgram "$out/bin/concierge" --prefix PATH : ${conciergeRuntime}/bin
          '';
        in
        {
          default = concierge;
          inherit concierge;
          tincan = transportPackage;
        });

      formatter = forEachSystem (pkgs: pkgs.writeShellApplication {
        name = "concierge-format";
        runtimeInputs = [ pkgs.golines pkgs.go_1_25 ];
        text = ''
          if [ "$#" -eq 0 ]; then
            set -- .
          fi
          exec golines ${golinesFlags} -w "$@"
        '';
      });

      checks = forEachSystem (pkgs:
        let
          packages = self.packages.${pkgs.stdenv.hostPlatform.system};
          transportPackage = tincan.packages.${pkgs.stdenv.hostPlatform.system}.tincan;
          healthHerdr = pkgs.writeShellScript "concierge-health-herdr" ''
            case "$1 $2" in
              'pane get') printf '{"result":{"pane":{"pane_id":"p1","foreground_cwd":"%s","workspace_id":"w1","tab_id":"t1"}}}\n' "$SMOKE_WORKTREE" ;;
              'agent get') printf '%s\n' '{"result":{"agent":{"pane_id":"p1","agent_status":"working","agent":"codex"}}}' ;;
              'pane process-info') printf '{"result":{"type":"pane_process_info","process_info":{"pane_id":"p1","shell_pid":%s,"foreground_processes":%s}}}\n' "$SMOKE_SHELL_PID" "$SMOKE_FOREGROUND" ;;
              'status --json') printf '%s\n' '{"server":{"running":true}}' ;;
              *) exit 71 ;;
            esac
          '';
        in
        {
          inherit (packages) concierge;
          go-source = pkgs.runCommand "concierge-go-source" { src = (goModule pkgs).src; } ''
            test -e "$src/go.mod"
            test -e "$src/cmd"
            test -e "$src/pkg"
            test ! -e "$src/README.md"
            touch "$out"
          '';
          concierge-smoke = pkgs.runCommand "concierge-smoke" { } ''
            unset CONCIERGE_HOME TINCAN_HOME TINCAN_HERDR HERDR_SESSION HERDR_SOCKET_PATH HERDR_WORKSPACE_ID HERDR_TAB_ID HERDR_PANE_ID
            export PATH=
            test -x ${packages.concierge}/bin/concierge
            test "$(${packages.concierge}/bin/concierge --version)" = 0.1.0
            set -- ${packages.concierge}/bin/*
            test "$#" -eq 1
            test "$1" = ${packages.concierge}/bin/concierge
            set -- ${transportPackage}/bin/*
            test "$#" -eq 1
            test "$1" = ${transportPackage}/bin/tincan
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" state init > initialized
            ${pkgs.gnugrep}/bin/grep -qx 'version: 2' initialized
            ${pkgs.coreutils}/bin/cp ${pkgs.bash}/bin/bash "$TMPDIR/codex"
            "$TMPDIR/codex" -e <<'INTAKE'
            ${pkgs.jq}/bin/jq -n --arg cwd "$TMPDIR" \
              '{cwd:$cwd,session_id:"packaged-intake",hook_event_name:"SessionStart"}' | \
              ${packages.concierge}/bin/concierge --root "$TMPDIR/state" primary claim >/dev/null
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" host serve > "$TMPDIR/host-output" &
            host_pid=$!
            trap 'kill "$host_pid" 2>/dev/null || true; wait "$host_pid" 2>/dev/null || true' EXIT
            for attempt in {1..100}; do
              test ! -S "$TMPDIR/state/host.sock" || break
              kill -0 "$host_pid"
              ${pkgs.coreutils}/bin/sleep 0.01
            done
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" backlog add smoke-a1 'Packaged intake' \
              --kind task --repo exmachina >/dev/null
            INTAKE
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" backlog list > backlog
            ${pkgs.gnugrep}/bin/grep -Fx '  "smoke-a1",queued,"task","exmachina","Packaged intake"' backlog
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" backlog show smoke-a1 --full > detail
            ${pkgs.gnugrep}/bin/grep -Fx '  title: "Packaged intake"' detail
            ${transportPackage}/bin/tincan --root "$TMPDIR/messages" inbox send concierge --key wake --body 'Packaged wake' >/dev/null
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" --message-root "$TMPDIR/messages" > brief
            ${pkgs.gnugrep}/bin/grep -Fx 'wakes: 1 pending for concierge' brief
            ${pkgs.gnugrep}/bin/grep -Fx 'backlog: 1 queued' brief
            ${packages.concierge}/bin/concierge task reply --help >/dev/null
            ${packages.concierge}/bin/concierge primary --help >/dev/null
            ${packages.concierge}/bin/concierge control --help >/dev/null
            ${packages.concierge}/bin/concierge supervise --help >/dev/null

            export SMOKE_WORKTREE="$TMPDIR"
            # Health checks exclude PID 1, which Nix uses for the builder shell.
            "$TMPDIR/codex" -e <<'HEALTH'
            export SMOKE_SHELL_PID=$$
            (exec -a codex ${pkgs.coreutils}/bin/coreutils --coreutils-prog=sleep 60) &
            agent_pid=$!
            export SMOKE_FOREGROUND='[]'
            ${pkgs.lib.optionalString pkgs.stdenv.isDarwin ''
              # Darwin's Nix sandbox blocks /bin/ps; native acceptance covers it.
              SMOKE_FOREGROUND="$(printf '[{"pid":%s,"name":"codex"}]' "$agent_pid")"
            ''}
            trap 'kill "''${host_pid:-}" "$agent_pid" 2>/dev/null || true; wait "''${host_pid:-}" "$agent_pid" 2>/dev/null || true' EXIT
            ${pkgs.jq}/bin/jq --arg worktree "$TMPDIR" \
              '.state = "in_flight" | .worker = {name:"smoke",harness:"codex",session:"smoke",workspace:"w1",tab:"t1",pane:"p1",project:$worktree,worktree:$worktree,generation:"gen-smoke",agent_args:["resume","smoke"]}' \
              "$TMPDIR/state/records/smoke-a1.json" > record
            ${pkgs.coreutils}/bin/mv record "$TMPDIR/state/records/smoke-a1.json"
            ${pkgs.jq}/bin/jq -n --arg cwd "$TMPDIR" \
              '{cwd:$cwd,session_id:"packaged-health",hook_event_name:"SessionStart"}' | \
              ${packages.concierge}/bin/concierge --root "$TMPDIR/state" primary claim >/dev/null
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" --message-root "$TMPDIR/messages" \
              host --herdr ${healthHerdr} serve > "$TMPDIR/health-host-output" &
            host_pid=$!
            for attempt in {1..100}; do
              if ${pkgs.gnugrep}/bin/grep -q '^status: ready$' "$TMPDIR/health-host-output"; then break; fi
              kill -0 "$host_pid"
              ${pkgs.coreutils}/bin/sleep 0.01
            done
            ${pkgs.gnugrep}/bin/grep -q '^status: ready$' "$TMPDIR/health-host-output"
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" --message-root "$TMPDIR/messages" \
              supervise --herdr ${healthHerdr} task smoke-a1 > supervision
            ${pkgs.gnugrep}/bin/grep -qx 'supervision: active' supervision
            ${packages.concierge}/bin/concierge --root "$TMPDIR/state" supervise stop task smoke-a1 > stopped
            ${pkgs.gnugrep}/bin/grep -qx 'supervision: stopped' stopped
            HEALTH
            : > "$out"
          '';
          go-format = pkgs.runCommand "concierge-go-format" {
            nativeBuildInputs = [ pkgs.golines pkgs.go_1_25 ];
            src = goSource pkgs;
          } ''
            cd "$src"
            unformatted="$(golines ${golinesFlags} -l .)"
            if [ -n "$unformatted" ]; then
              printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
              exit 1
            fi
            touch "$out"
          '';
          go-tests = buildCommands pkgs;
        });

      devShells = forEachSystem (pkgs:
        {
          default = pkgs.mkShell {
            packages = [ pkgs.go_1_25 pkgs.gopls pkgs.golines herdr.packages.${pkgs.stdenv.hostPlatform.system}.default ];
          };
        });
    };
}
