#!/bin/sh

PI_PACKAGE="@earendil-works/pi-coding-agent"
PI_CMD="pi"
PI_ESC=$(printf '\033')
PI_CR=$(printf '\r')
readonly PI_PACKAGE PI_CMD PI_ESC PI_CR

pi_installer_main() {
  set -eu

  check_file="${TMPDIR:-/tmp}/pi-installer-checks.$$"
  run_preflight_checks >"$check_file" &
  check_pid=$!

  pi_logo_animation

  if wait "$check_pid"; then
    check_status=0
  else
    check_status=$?
  fi

  printf '\033[1m  Pi Installer\033[0m\n\033[2m  There are many coding agents, but this one is mine.\033[0m\n\n'
  if [ "$check_status" -eq 0 ]; then
    cat "$check_file"
  fi
  rm -f "$check_file"

  if [ "$check_status" -ne 0 ]; then
    if ! install_node_npm_interactive; then
      exit "$check_status"
    fi

    check_file="${TMPDIR:-/tmp}/pi-installer-checks.$$"
    if run_preflight_checks >"$check_file"; then
      check_status=0
    else
      check_status=$?
    fi
    cat "$check_file"
    rm -f "$check_file"

    if [ "$check_status" -ne 0 ]; then
      exit "$check_status"
    fi
  fi

  printf 'This will run:\n\n  npm install -g %s\n\n' "$PI_PACKAGE"
  confirm_install

  printf '\n'
  install_pi_package
  printf '\nPi was installed successfully.\n'
  if command -v "$PI_CMD" >/dev/null 2>&1; then
    printf '\nRun it with: pi\n'
    if [ "${PI_NODE_INSTALLED_STANDALONE:-0}" = 1 ]; then
      printf 'If pi is not found in your shell yet, add this to your shell profile:\n\n'
      printf '  export PATH="%s:$PATH"\n' "$PI_STANDALONE_NODE_BIN"
    fi
  else
    cat <<'EOF'
The pi command was installed, but it is not on your PATH yet.
Check npm's global bin directory with:

  npm bin -g

Then add that directory to your shell PATH.
EOF
  fi

}

run_preflight_checks() {
  status=0
  yellow="${PI_ESC}[33m"
  reset="${PI_ESC}[0m"

  if command -v node >/dev/null 2>&1; then
    node_version=$(node --version)
    if ! node -e 'const [maj,min,patch] = process.versions.node.split(".").map(Number); process.exit(maj > 20 || (maj === 20 && (min > 6 || (min === 6 && patch >= 0))) ? 0 : 1)' >/dev/null; then
      printf 'error: Pi requires Node.js 20.6.0 or newer. Found %s.\n' "$node_version"
      status=1
    fi
  else
    printf 'error: Node.js 20.6.0 or newer is required to install Pi.\n'
    status=1
  fi

  if ! command -v npm >/dev/null 2>&1; then
    printf 'error: npm is required to install Pi.\n'
    status=1
  fi

  if [ "$status" -ne 0 ]; then
    printf '\n'
  fi

  if pi_path=$(command -v "$PI_CMD" 2>/dev/null); then
    printf '%sExisting pi found at: %s%s\n' "$yellow" "$pi_path" "$reset"
    "$PI_CMD" --version 2>/dev/null | sed "s/^/${yellow}/; s/\$/${reset}/" || true
    printf '\n'
  fi

  return "$status"
}

install_node_npm_interactive() {
  method=$(detect_node_install_method)
  case "$method" in
    homebrew) label="Homebrew" ;;
    apt) label="apt" ;;
    apk) label="apk" ;;
    standalone) label="standalone Node.js" ;;
  esac

  if ! ( : <>/dev/tty ) 2>/dev/null; then
    printf 'No terminal detected; install Node.js 20.6.0 or newer and npm, then run this installer again.\n'
    return 1
  fi
  exec 3<>/dev/tty

  printf 'Pi needs Node.js 20.6.0 or newer and npm. Install them now with %s? [Y/n] ' "$label" >&3
  if ! IFS= read -r answer <&3; then
    answer=
  fi
  exec 3>&-
  case "$answer" in
    n|N|no|NO) printf '\nInstall Node.js 20.6.0 or newer and npm, then run this installer again.\n'; return 1 ;;
    *) ;;
  esac

  install_node_npm "$method" "$label"
}

detect_node_install_method() {
  case "$(uname -s)" in
    Darwin)
      if command -v brew >/dev/null 2>&1; then
        printf 'homebrew'
      else
        printf 'standalone'
      fi
      ;;
    Linux)
      if command -v apt-cache >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1 && apt_node_candidate_is_new_enough; then
        printf 'apt'
      elif command -v apk >/dev/null 2>&1 && apk_node_candidate_is_new_enough; then
        printf 'apk'
      else
        printf 'standalone'
      fi
      ;;
    *)
      printf 'standalone'
      ;;
  esac
}

apt_node_candidate_is_new_enough() {
  version=$(apt-cache policy nodejs 2>/dev/null | awk '/Candidate:/ { print $2; exit }')
  [ -n "$version" ] && [ "$version" != "(none)" ] && node_version_string_is_new_enough "$version"
}

apk_node_candidate_is_new_enough() {
  version=$(apk search -x nodejs 2>/dev/null | awk -F- '/^nodejs-/ { print $2; exit }')
  [ -n "$version" ] && node_version_string_is_new_enough "$version"
}

node_version_string_is_new_enough() {
  version="${1#v}"
  case "$version" in
    [0-9]*) ;;
    *) return 1 ;;
  esac
  version="${version%%[!0-9.]*}"
  version_ifs=${IFS- }
  IFS=.
  set -- $version
  IFS=$version_ifs
  major="${1:-}"
  minor="${2:-0}"
  patch="${3:-0}"
  case "$major" in ''|*[!0-9]*) return 1 ;; esac
  case "$minor" in ''|*[!0-9]*) minor=0 ;; esac
  case "$patch" in ''|*[!0-9]*) patch=0 ;; esac

  [ "$major" -gt 20 ] && return 0
  [ "$major" -eq 20 ] && [ "$minor" -gt 6 ] && return 0
  [ "$major" -eq 20 ] && [ "$minor" -eq 6 ] && [ "$patch" -ge 0 ] && return 0
  return 1
}

install_node_npm() {
  method="$1"; label="$2"

  if [ -t 1 ] && [ "${TERM:-}" != "dumb" ]; then
    install_node_npm_with_progress "$method" "$label"
  else
    printf '\nInstalling Node.js and npm with %s...\n\n' "$label"
    run_node_install_method "$method"
    printf '\nNode.js and npm are installed.\n'
  fi

  if [ "$method" = standalone ]; then
    load_standalone_node
    PI_NODE_INSTALLED_STANDALONE=1
  fi
  hash -r
  printf '\n'
}

install_node_npm_with_progress() {
  method="$1"; label="$2"
  log_file="${TMPDIR:-/tmp}/pi-installer-node.$$"
  rm -f "$log_file"
  : >"$log_file"

  run_node_install_method "$method" >"$log_file" 2>&1 &
  install_pid=$!

  printf '\033[?25l'
  animate_node_install "$log_file" "$label" &
  progress_pid=$!
  trap 'kill "$install_pid" 2>/dev/null || true; finish_install_progress "$progress_pid"; exit 130' INT TERM

  if wait "$install_pid"; then
    status=0
  else
    status=$?
  fi

  finish_install_progress "$progress_pid"
  trap - INT TERM

  if [ "$status" -ne 0 ]; then
    printf '\033[31mNode.js installation failed.\033[0m\n\n'
    cat "$log_file"
    rm -f "$log_file"
    return "$status"
  fi

  rm -f "$log_file"
  if terminal_supports_unicode; then
    printf '  \033[32m✓\033[0m Node.js and npm install complete\n'
  else
    printf '  \033[32mok\033[0m Node.js and npm install complete\n'
  fi
}

run_node_install_method() {
  case "$1" in
    homebrew) install_node_with_homebrew ;;
    apt) install_node_with_apt ;;
    apk) install_node_with_apk ;;
    standalone) install_node_standalone ;;
  esac
}

install_node_with_homebrew() {
  if brew list node >/dev/null 2>&1; then
    brew upgrade node
  else
    brew install node
  fi
}

install_node_with_apt() {
  print_sudo_note
  if [ "${EUID:-$(id -u)}" -eq 0 ]; then
    apt-get update
    apt-get install -y nodejs npm
  else
    sudo sh -c 'apt-get update && apt-get install -y nodejs npm'
  fi
}

install_node_with_apk() {
  print_sudo_note
  run_with_sudo apk add --update-cache nodejs npm
}

install_node_standalone() {
  node_platform=$(detect_node_binary_platform) || {
    printf 'Unsupported operating system for automatic Node.js install: %s\n' "$(uname -s)"
    return 1
  }
  node_arch=$(detect_node_binary_arch) || {
    printf 'Unsupported CPU architecture for automatic Node.js install: %s\n' "$(uname -m)"
    return 1
  }
  node_dist_base="https://nodejs.org/dist/latest-v22.x"
  node_base_dir=$(node_standalone_base_dir)
  node_tmp_dir="${TMPDIR:-/tmp}/pi-node.$$"

  rm -rf "$node_tmp_dir"
  mkdir -p "$node_tmp_dir" "$node_base_dir"

  printf 'Resolving Node.js binary for %s-%s\n' "$node_platform" "$node_arch"
  curl -fsSL "$node_dist_base/SHASUMS256.txt" -o "$node_tmp_dir/SHASUMS256.txt"
  node_file=$(awk -v suffix="-$node_platform-$node_arch.tar.xz" '
    index($2, "node-v") == 1 && length($2) >= length(suffix) && substr($2, length($2) - length(suffix) + 1) == suffix { print $2; exit }
  ' "$node_tmp_dir/SHASUMS256.txt")
  if [ -z "$node_file" ]; then
    printf 'No Node.js binary is available for %s-%s.\n' "$node_platform" "$node_arch"
    rm -rf "$node_tmp_dir"
    return 1
  fi

  printf 'Downloading Node.js %s\n' "${node_file%.tar.xz}"
  curl -fsSL "$node_dist_base/$node_file" -o "$node_tmp_dir/$node_file"
  verify_node_standalone_download "$node_tmp_dir" "$node_file"
  ensure_node_standalone_extract_tools "$node_platform"

  node_dir="$node_base_dir/${node_file%.tar.xz}"
  rm -rf "$node_dir"
  printf 'Extracting Node.js to %s\n' "$node_dir"
  tar -xf "$node_tmp_dir/$node_file" -C "$node_base_dir"
  rm -f "$node_base_dir/current"
  ln -s "$node_dir" "$node_base_dir/current"
  rm -rf "$node_tmp_dir"
  printf 'Node.js installed at %s\n' "$node_dir"
}

verify_node_standalone_download() {
  checksum_dir="$1"
  checksum_file_name="$2"
  awk -v file="$checksum_file_name" '$2 == file { print }' "$checksum_dir/SHASUMS256.txt" > "$checksum_dir/SHASUMS256.selected"

  if command -v sha256sum >/dev/null 2>&1; then
    printf 'Verifying Node.js download\n'
    (cd "$checksum_dir" && sha256sum -c SHASUMS256.selected)
  elif command -v shasum >/dev/null 2>&1; then
    printf 'Verifying Node.js download\n'
    (cd "$checksum_dir" && shasum -a 256 -c SHASUMS256.selected)
  fi
}

ensure_node_standalone_extract_tools() {
  extract_platform="$1"

  if [ "$extract_platform" = linux ] && ! command -v xz >/dev/null 2>&1; then
    printf 'Installing xz-utils for Node.js archive extraction\n'
    print_sudo_note
    if command -v apt-get >/dev/null 2>&1; then
      run_with_sudo apt-get update
      run_with_sudo apt-get install -y xz-utils
    elif command -v apk >/dev/null 2>&1; then
      run_with_sudo apk add --update-cache xz
    else
      printf 'xz is required to extract Node.js. Install xz and run this installer again.\n'
      return 1
    fi
  fi
}

load_standalone_node() {
  PI_STANDALONE_NODE_BIN="$(node_standalone_base_dir)/current/bin"
  PATH="$PI_STANDALONE_NODE_BIN:$PATH"
  export PI_STANDALONE_NODE_BIN PATH
}

node_standalone_base_dir() {
  if [ -n "${XDG_DATA_HOME:-}" ]; then
    printf '%s/pi-node' "$XDG_DATA_HOME"
  else
    printf '%s/.local/share/pi-node' "$HOME"
  fi
}

detect_node_binary_platform() {
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    *) return 1 ;;
  esac
}

detect_node_binary_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'x64' ;;
    arm64|aarch64) printf 'arm64' ;;
    armv7l) printf 'armv7l' ;;
    ppc64le) printf 'ppc64le' ;;
    s390x) printf 's390x' ;;
    *) return 1 ;;
  esac
}

print_sudo_note() {
  if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    printf 'This may ask for your sudo password.\n\n'
  fi
}

run_with_sudo() {
  if [ "${EUID:-$(id -u)}" -eq 0 ]; then
    "$@"
  else
    sudo "$@"
  fi
}

confirm_install() {
  if ! ( : <>/dev/tty ) 2>/dev/null; then
    printf 'No terminal detected; continuing without confirmation.\n'
    return 0
  fi
  exec 3<>/dev/tty

  printf 'Continue? [Y/n] ' >&3
  if ! IFS= read -r answer <&3; then
    answer=
  fi
  exec 3>&-
  case "$answer" in
    n|N|no|NO) printf '\nInstallation cancelled.\n'; exit 0 ;;
    *) ;;
  esac
}

install_pi_package() {
  if [ -t 1 ] && [ "${TERM:-}" != "dumb" ]; then
    install_pi_package_with_progress
  else
    printf 'Installing Pi...\n\n'
    npm install -g --no-fund --no-audit --loglevel=error --progress=false "$PI_PACKAGE"
  fi
}

install_pi_package_with_progress() {
  log_file="${TMPDIR:-/tmp}/pi-installer-npm.$$"
  rm -f "$log_file"
  : >"$log_file"

  npm install -g --no-fund --no-audit --loglevel=verbose --progress=false "$PI_PACKAGE" >"$log_file" 2>&1 &
  npm_pid=$!

  printf '\033[?25l'
  animate_npm_install "$log_file" &
  progress_pid=$!
  trap 'kill "$npm_pid" 2>/dev/null || true; finish_install_progress "$progress_pid"; exit 130' INT TERM

  if wait "$npm_pid"; then
    status=0
  else
    status=$?
  fi

  finish_install_progress "$progress_pid"
  trap - INT TERM

  if [ "$status" -ne 0 ]; then
    printf '\033[31mInstallation failed.\033[0m\n\n'
    cat "$log_file"
    rm -f "$log_file"
    return "$status"
  fi

  rm -f "$log_file"
  if terminal_supports_unicode; then
    printf '  \033[32m✓\033[0m npm install complete\n'
  else
    printf '  \033[32mok\033[0m npm install complete\n'
  fi
}

finish_install_progress() {
  progress_pid="$1"

  kill "$progress_pid" 2>/dev/null || true
  wait "$progress_pid" 2>/dev/null || true
  printf '\r\033[K\033[?25h'
}

terminal_supports_unicode() {
  locale="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"

  case "$locale" in
    *UTF-8*|*utf-8*|*UTF8*|*utf8*) return 0 ;;
  esac

  case "${TERM_PROGRAM:-}" in
    Apple_Terminal|iTerm.app|vscode|WezTerm) return 0 ;;
  esac

  return 1
}

spinner_frame() {
  frame_step="$1"
  frame_count="$2"

  if [ "$frame_count" -eq 10 ]; then
    case $((frame_step % 10)) in
      0) printf '⠋' ;;
      1) printf '⠙' ;;
      2) printf '⠹' ;;
      3) printf '⠸' ;;
      4) printf '⠼' ;;
      5) printf '⠴' ;;
      6) printf '⠦' ;;
      7) printf '⠧' ;;
      8) printf '⠇' ;;
      *) printf '⠏' ;;
    esac
  else
    case $((frame_step % 4)) in
      0) printf '-' ;;
      1) printf '\\' ;;
      2) printf '|' ;;
      *) printf '/' ;;
    esac
  fi
}

animate_npm_install() {
  log_file="$1"

  if terminal_supports_unicode; then
    full="█"
    empty="░"
    frame_count=10
  else
    full="#"
    empty="-"
    frame_count=4
  fi

  step=0
  label="starting npm install"
  while :; do
    frame=$(spinner_frame "$step" "$frame_count")
    if [ $((step % 5)) -eq 0 ]; then
      label=$(npm_install_progress_label "$log_file" "$label")
    fi
    draw_install_progress "$step" "$frame" "$label" "$full" "$empty"
    step=$((step + 1))
    sleep 0.08
  done
}

animate_node_install() {
  log_file="$1"
  method_label="$2"

  if terminal_supports_unicode; then
    full="█"
    empty="░"
    frame_count=10
  else
    full="#"
    empty="-"
    frame_count=4
  fi

  step=0
  label="starting ${method_label} install"
  while :; do
    frame=$(spinner_frame "$step" "$frame_count")
    if [ $((step % 5)) -eq 0 ]; then
      label=$(node_install_progress_label "$log_file" "$label")
    fi
    draw_install_progress "$step" "$frame" "$label" "$full" "$empty" "Installing Node.js"
    step=$((step + 1))
    sleep 0.08
  done
}

node_install_progress_label() {
  log_file="$1"
  label="$2"

  while IFS= read -r line; do
    line=${line##*"$PI_CR"}
    case "$line" in
      "") ;;
      Resolving\ Node.js*) label="resolving Node.js binary" ;;
      Downloading\ Node.js*) label="$line" ;;
      Verifying\ Node.js*) label="verifying download" ;;
      Installing\ xz-utils*) label="installing xz-utils" ;;
      Extracting\ Node.js*) label="extracting Node.js" ;;
      Node.js\ installed*) label="Node.js installed" ;;
      Hit:*|Get:*|Ign:*) label="updating package lists" ;;
      Reading\ package\ lists*) label="reading package lists" ;;
      Building\ dependency\ tree*) label="resolving dependencies" ;;
      The\ following\ NEW\ packages*) label="installing dependencies" ;;
      Need\ to\ get*|Fetched\ *) label="$line" ;;
      Selecting\ previously\ unselected\ package*) label="selecting packages" ;;
      Preparing\ to\ unpack*) label="preparing packages" ;;
      Unpacking\ *|Setting\ up\ *) label="$line" ;;
      fetch\ *) label="fetching packages" ;;
      *Installing\ nodejs*) label="$line" ;;
      OK:\ *) label="$line" ;;
      ==\>\ Downloading*) label="downloading packages" ;;
      ==\>\ Installing*|==\>\ Upgrading*) label="$line" ;;
      ==\>\ Pouring*) label="installing package" ;;
      *already\ installed*) label="$line" ;;
    esac
  done < "$log_file"

  if [ "${#label}" -gt 64 ]; then
    label=$(printf '%.61s...' "$label")
  fi
  printf '%s' "$label"
}

npm_install_progress_label() {
  log_file="$1"
  label="$2"
  metadata_cache_count=0
  metadata_fetch_count=0
  tarball_cache_count=0
  tarball_fetch_count=0

  while IFS= read -r line; do
    line=${line%"$PI_CR"}
    case "$line" in
      npm\ verbose\ title\ npm\ install*)
        label="resolving packages"
        ;;
      npm\ http\ fetch\ GET\ *https://registry.npmjs.org/*.tgz*)
        tarball_fetch_count=$((tarball_fetch_count + 1))
        label="fetching tarballs (${tarball_fetch_count})"
        ;;
      npm\ http\ cache\ *@https://registry.npmjs.org/*.tgz*)
        tarball_cache_count=$((tarball_cache_count + 1))
        if [ "$tarball_fetch_count" -gt 0 ]; then
          label="fetching tarballs (${tarball_fetch_count})"
        else
          label="checking tarballs (${tarball_cache_count})"
        fi
        ;;
      npm\ http\ fetch\ GET\ *https://registry.npmjs.org/*)
        metadata_fetch_count=$((metadata_fetch_count + 1))
        label="fetching package metadata (${metadata_fetch_count})"
        ;;
      npm\ http\ cache\ https://registry.npmjs.org/*)
        metadata_cache_count=$((metadata_cache_count + 1))
        if [ "$metadata_fetch_count" -gt 0 ]; then
          label="fetching package metadata (${metadata_fetch_count})"
        else
          label="checking cached metadata (${metadata_cache_count})"
        fi
        ;;
      npm\ info\ run\ *)
        rest=${line#npm info run }
        package=${rest%% *}
        rest=${rest#* }
        script=${rest%% *}
        package=${package%@*}
        case "$line" in
          *\{\ code:*) label="finished ${script} for ${package}" ;;
          *) label="running ${script} for ${package}" ;;
        esac
        ;;
      changed\ *|added\ *|removed\ *|updated\ *|up\ to\ date\ *)
        label="$line"
        ;;
    esac
  done < "$log_file"

  printf '%s' "$label"
}

draw_install_progress() {
  step="$1"; frame="$2"; label="$3"; full="$4"; empty="$5"; title="${6:-Installing Pi}"

  reset="${PI_ESC}[0m"
  dim="${PI_ESC}[2m"
  cyan="${PI_ESC}[38;2;71;217;250m"
  red="${PI_ESC}[38;2;216;59;48m"
  green="${PI_ESC}[38;2;102;247;65m"
  orange="${PI_ESC}[38;2;246;155;49m"
  bold="${PI_ESC}[1m"

  width=28
  trail=8
  head=$((step % (width + trail)))
  bar=""

  i=0
  while [ "$i" -lt "$width" ]; do
    age=$((head - i))
    if [ "$age" -ge 0 ] && [ "$age" -lt "$trail" ]; then
      case "$age" in
        0|1) cell="${green}${full}${reset}" ;;
        2|3) cell="${cyan}${full}${reset}" ;;
        4|5) cell="${red}${full}${reset}" ;;
        *) cell="${orange}${full}${reset}" ;;
      esac
    else
      cell="${dim}${empty}${reset}"
    fi
    bar="${bar}${cell}"
    i=$((i + 1))
  done

  printf '\r\033[K  %s%s%s %s %s%s%s %s' "$orange" "$frame" "$reset" "$bar" "$bold" "$title" "$reset" "$label"
}

pi_logo_animation() {
  if [ ! -t 1 ] || [ "${TERM:-}" = "dumb" ]; then
    print_static_logo
    return
  fi

  esc="${PI_ESC}["
  reset="${PI_ESC}[0m"
  hide="${esc}?25l"
  show="${esc}?25h"
  clear="${esc}H"

  printf '%s%s' "$hide" "${esc}2J${esc}H"

  for y in 0 1 2 3; do draw_logo_frame "$clear" "$reset" 0 left 2 "$y" 0 0; sleep 0.075; done
  for y in 0 1 2; do draw_logo_frame "$clear" "$reset" 1 top 2 "$y" 0 0; sleep 0.075; done
  for y in 0 1 2 3 4; do draw_logo_frame "$clear" "$reset" 2 right 5 "$y" 0 0; sleep 0.075; done

  draw_logo_frame "$clear" "$reset" 3 none 0 0 0 0; sleep 0.25
  draw_logo_frame "$clear" "$reset" 3 none 0 0 1 0; sleep 0.08
  draw_logo_frame "$clear" "$reset" 3 none 0 0 0 0; sleep 0.08
  draw_logo_frame "$clear" "$reset" 3 none 0 0 1 0; sleep 0.08
  draw_logo_frame "$clear" "$reset" 4 none 0 0 0 0; sleep 0.10
  draw_logo_frame "$clear" "$reset" 5 none 0 0 0 0; sleep 0.45
  draw_logo_frame "$clear" "$reset" 5 none 0 0 0 1; sleep 0.12
  draw_logo_frame "$clear" "$reset" 5 none 0 0 0 0; sleep 0.12
  draw_logo_frame "$clear" "$reset" 5 none 0 0 0 1; sleep 0.45

  printf '%s%s\n' "$reset" "$show"
}

draw_logo_frame() {
  clear="$1"; reset="$2"; phase="$3"; active="$4"; ax="$5"; ay="$6"; flash="$7"; white="$8"

  left=0
  top=0

  panel_cell="${PI_ESC}[38;2;17;30;42m██"
  cyan_cell="${PI_ESC}[38;2;71;217;250m██"
  red_cell="${PI_ESC}[38;2;216;59;48m██"
  green_cell="${PI_ESC}[38;2;102;247;65m██"
  orange_cell="${PI_ESC}[38;2;246;155;49m██"
  white_cell="${PI_ESC}[38;2;255;255;255m██"
  flash_cell="${PI_ESC}[38;2;255;245;180m██"

  pad=$(repeat_space "$left")
  clear_cell="$panel_cell"
  frame="$clear"
  i=0
  while [ "$i" -lt "$top" ]; do frame="${frame}\n"; i=$((i + 1)); done

  for y in 0 1 2 3 4 5 6 7 8; do
    frame="${frame}${pad}"
    for x in 1 2 3 4 5 6 7 8; do
      set_logo_cell_color "$phase" "$active" "$ax" "$ay" "$flash" "$white" "$y" "$x"
      case "$LOGO_COLOR" in
        cyan) cell="$cyan_cell" ;;
        red) cell="$red_cell" ;;
        green) cell="$green_cell" ;;
        orange) cell="$orange_cell" ;;
        white) cell="$white_cell" ;;
        flash) cell="$flash_cell" ;;
        *) cell="$clear_cell" ;;
      esac
      frame="${frame}${cell}"
    done
    frame="${frame}${reset}\n"
  done
  printf '%b' "$frame"
}

set_logo_cell_color() {
  phase="$1"; active="$2"; ax="$3"; ay="$4"; flash="$5"; white="$6"; y="$7"; x="$8"

  if [ "$white" = 1 ]; then
    if in_cells "$y" "$x" "3,2 3,3 3,4 4,2 4,4 5,2 5,3 5,5 6,2 6,5"; then LOGO_COLOR=white; else LOGO_COLOR=panel; fi
    return
  fi
  if [ "$flash" = 1 ] && [ "$y" = 6 ] && [ "$x" -ge 1 ] && [ "$x" -le 6 ]; then LOGO_COLOR=flash; return; fi

  case "$active" in
    left)  if in_piece "$y" "$x" "$ay" "$ax" "0,0 1,0 1,1 2,0"; then LOGO_COLOR=red; return; fi ;;
    top)   if in_piece "$y" "$x" "$ay" "$ax" "0,0 0,1 0,2 1,2"; then LOGO_COLOR=cyan; return; fi ;;
    right) if in_piece "$y" "$x" "$ay" "$ax" "0,0 1,0 2,0 2,1"; then LOGO_COLOR=green; return; fi ;;
  esac

  if [ "$phase" = 4 ]; then
    if in_cells "$y" "$x" "2,2 2,3 2,4 3,4"; then LOGO_COLOR=cyan; return; fi
    if in_cells "$y" "$x" "3,2 4,2 4,3 5,2"; then LOGO_COLOR=red; return; fi
    if in_cells "$y" "$x" "4,5 5,5"; then LOGO_COLOR=green; return; fi
    LOGO_COLOR=panel; return
  fi

  if [ "$phase" -ge 5 ]; then
    if in_cells "$y" "$x" "3,2 3,3 3,4 4,4"; then LOGO_COLOR=cyan; return; fi
    if in_cells "$y" "$x" "4,2 5,2 5,3 6,2"; then LOGO_COLOR=red; return; fi
    if in_cells "$y" "$x" "5,5 6,5"; then LOGO_COLOR=green; return; fi
    LOGO_COLOR=panel; return
  fi

  if [ "$phase" -le 3 ] && in_cells "$y" "$x" "6,1 6,2 6,3 6,4"; then LOGO_COLOR=orange; return; fi
  if [ "$phase" -ge 2 ] && in_cells "$y" "$x" "2,2 2,3 2,4 3,4"; then LOGO_COLOR=cyan; return; fi
  if [ "$phase" -ge 1 ] && in_cells "$y" "$x" "3,2 4,2 4,3 5,2"; then LOGO_COLOR=red; return; fi
  if [ "$phase" -ge 3 ] && in_cells "$y" "$x" "4,5 5,5 6,5 6,6"; then LOGO_COLOR=green; return; fi

  LOGO_COLOR=panel
}

in_piece() {
  y="$1"; x="$2"; py="$3"; px="$4"; cells="$5"
  for item in $cells; do
    dy=${item%,*}; dx=${item#*,}
    [ "$y" -eq $((py + dy)) ] && [ "$x" -eq $((px + dx)) ] && return 0
  done
  return 1
}

in_cells() {
  y="$1"; x="$2"; shift 2
  for item in $1; do
    [ "$item" = "$y,$x" ] && return 0
  done
  return 1
}

repeat_space() {
  count="$1"; out=""
  while [ "$count" -gt 0 ]; do out=" $out"; count=$((count - 1)); done
  printf '%s' "$out"
}

print_static_logo() {
  cat <<'EOF'

  ██████
  ██  ██
  ████  ██
  ██    ██

EOF
}

pi_installer_main "$@"
