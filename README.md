# psclassic-launcher

An unofficial launcher for **Planetside Classic** on Linux (and macOS with Wine).
It does what the official Windows launcher does, natively: download and check
the game files, change the game's settings, and start the game with Proton or
Wine. It is not made or supported by the Planetside Classic team.

There are two programs:

| Program | What it is |
|---|---|
| `psclassic-launcher` | The launcher. With no arguments it opens a window laid out like the Windows launcher. With a command (`play`, `update`, `settings`, ...) it works in a terminal. |
| `psclassic-updater` | Only the update step, as one static binary with no dependencies. Made for Heroic's "run before the game" script. |

## Download

Each [release](https://github.com/ltsjoe/psclassic-launcher/releases) has two builds
for x86_64 Linux. Pick one:

| File | What it is |
|---|---|
| `psclassic-launcher-<version>-x86_64.AppImage` | The launcher as one file. Runs on any distro with glibc 2.35 or newer. Make it executable and run it. |
| `psclassic-launcher-<version>-steamrt4-x86_64.tar.gz` | Both programs, built in Valve's **Steam Linux Runtime 4.0** SDK. Runs inside the Steam Linux Runtime 4.0 container, or directly on a distro with glibc 2.38 or newer (SteamOS, Debian 13, Ubuntu 24.04, current Fedora and Arch). Also has `psclassic-updater` for Heroic. |

`SHA256SUMS` in the release lists the checksums. Check a download with
`sha256sum -c SHA256SUMS --ignore-missing`.

## Why

The official launcher (`PlanetsideClassic.exe`) checks the signature on its update list
with ECDSA through Windows CNG (`CngKey.Import` + `ECDsaCng.VerifyData`). Under
Wine that check can't run:

- With Wine's built-in .NET (wine-mono), `CngKey` and `ECDsaCng` are unimplemented stubs.
  The launcher says *"Could not check for updates: The method or operation is not implemented."*
- With Microsoft .NET installed in the prefix (winetricks `dotnet46`/`dotnet48`), Wine's
  `ncrypt.dll` can't import ECDSA keys (`fixme:ncrypt:NCryptImportKey Unhandled key magic 0x31534345`).
  The launcher says *"The update list is not signed by Planetside Classic. Nothing was changed."*

The list is signed correctly. Wine just can't check it, and the launcher only
enables PLAY after a successful check. This launcher does the same check natively.

## The window

The main window has the same parts in the same places as the Windows launcher:
server status and news, the game folder, *Check for updates* / *Update*,
*Save login from game*, *Settings* and **PLAY**. PLAY lights up once the game is
installed and up to date.

**Settings** has the Windows launcher's options (frame rate limit, indoor weather
fix, dark nights, Darklight colour, HD pack, beta branch, the saved login, voice
chat keys, devices and volumes, the flashlight and its key) plus a **Linux** row:
which Proton or Wine runs the game, and the prefix. Fyne can't see the side mouse
buttons, so to set Mouse 4 or 5 as a key, right-click the key button.

**PLAY** does what the Windows launcher does, then runs the game through Proton or Wine:

1. writes your settings into the game's `fpscap.ini` (read by the Classic team's
   client add-on, `dinput8.dll`);
2. starts `planetside.exe /CC /K:<code> /N:<account>` with a saved login, or
   `planetside.exe /K:StagingTest /CC` for the normal login screen;
3. with voice chat on, writes `voice\voice.ini` and starts
   `voice\PlanetsideClassicVoice.exe --with-game` once the game is up;
4. hides its window while you play, and comes back when the game closes.

Proton and Wine output goes to `~/.cache/psclassic-launcher/game.log`.

The Planetside Classic logo is not part of this program. The window draws a plain
title instead. To see the artwork, put a `logo.png` (840x250) in the settings folder.

## The terminal

```sh
psclassic-launcher                # opens the window (or this help without a display)
psclassic-launcher play           # check for updates, ask to install them, then PLAY
psclassic-launcher check          # what would change (writes nothing)
psclassic-launcher update         # install or update
psclassic-launcher settings       # list settings
psclassic-launcher settings fps=240 hd=off flashlight_key=F9
psclassic-launcher login          # save a login (asks for the code without showing it)
psclassic-launcher login -from-game   # take it from the game's chat.log, like the button
psclassic-launcher logout
psclassic-launcher runners        # Proton and Wine builds found on this machine
psclassic-launcher status         # players online and the news
```

`play -yes` installs updates without asking, `play -no-update` skips the check,
`play -detach` returns once the game has started. `-config FILE` before a command
uses another settings file. `psclassic-launcher help` lists everything.

## First start

The settings file is `~/.config/psclassic-launcher/launcher.ini`
(`~/Library/Application Support/psclassic-launcher/` on macOS). It uses the Windows
launcher's format and key names. The launcher needs no other game launcher: it
runs the game in **its own prefix**, `~/.local/share/psclassic-launcher/prefix`,
which Proton or Wine sets up the first time the game starts. On the first start it
fills in the settings file:

- **Proton or Wine:** it looks in Steam's folders (`compatibilitytools.d`, where
  ProtonPlus and ProtonUp-Qt install GE-Proton, and Valve's Proton), Lutris's and
  Heroic's (native or Flatpak each), and for Wine on the PATH. It picks the newest
  GE-Proton, else the newest numbered Valve Proton, else another Proton build, else
  Wine. `psclassic-launcher runners` lists them all, and Settings can switch.
- **The game:** if Heroic, Lutris or the Windows launcher already installed it (in
  their entries, `~/Games/*`, Heroic's prefixes, `~/.wine` or Steam's `compatdata`),
  those files are used where they are, so nothing is downloaded again. Otherwise it
  installs to `~/Games/Planetside Classic`. The game folder sits outside the
  launcher's prefix, so a broken prefix can be deleted and made again without
  downloading the game again.
- **Windows launcher settings:** if the game came with the Windows launcher's
  `launcher.ini`, its settings are taken over. The saved login is not: on Windows it
  is encrypted for the Windows account, so save it again here.

## The saved login code

The login code lets PLAY skip the login screen. On Windows the launcher protects it
with DPAPI. Here it is encrypted with AES-256-GCM, and the key is a random 32 bytes
kept in the desktop's keyring: KDE Wallet or GNOME Keyring (through the Secret
Service), or the macOS Keychain. `launcher.ini` only holds the encrypted code.

The protection is "only you, on this machine": a copy of `launcher.ini` is useless
elsewhere, and the keyring is locked with your login. Other programs running as you
can still ask an unlocked keyring for the key. Linux has no way to tie a secret to one
unsandboxed program. A Flatpak build using the Secret portal would keep it from other
sandboxed apps, but not from programs running outside a sandbox.

## The client add-on (dinput8.dll)

The Classic team's add-on is a `dinput8.dll` in the game folder. Wine has its own
`dinput8`, so the launcher starts the game with `WINEDLLOVERRIDES=dinput8=n,b`: the
game folder's copy first, Wine's after. A test run with `WINEDEBUG=+loaddll` showed
the add-on loaded as `native`.

If you start the game from Heroic or Lutris instead, add the same
environment variable to that entry.

A game folder reached through a prefix's `dosdevices/c:` link is turned into the real
`drive_c/...` path first. A `c:` in the middle of a Windows path breaks the game:
`PSCCX.ocx` can't find itself and the game stops with *"Runtime error 217"*.

## Heroic: update on every launch

Heroic can run a program before a game starts, and tells it the game's prefix in
`HEROIC_GAME_PREFIX`. With no flags, `psclassic-updater` uses that prefix.

1. Install it somewhere the Heroic Flatpak can see, e.g. `~/.local/bin/psclassic-updater`
   (`scripts/install.sh` does this).
2. Point a Heroic entry at the game itself, not at the launcher. Use
   `C:\Games\Planetside Classic\planetside.exe` in the same prefix, with the
   arguments `/K:StagingTest /CC` (what the launcher passes when no login is saved).
3. In that entry's settings, open the *Advanced* tab. Under *Scripts:*, set **"Select a script
   to run before the game is launched"** to `~/.local/bin/psclassic-updater`.

Heroic starts the game even if the update fails (it ignores the script's exit code).
The output is in the game's log in Heroic. Run `psclassic-updater -help` for its flags.

## How updating works

The same steps as the launcher's *Check for updates* and *Update*:

1. Downloads `manifest.txt` and `manifest.sig` from `https://patch.blackarmoredreapers.com`
   (or `beta/manifest.txt` on the beta branch, falling back to the release) and checks the
   signature (ECDSA P-256 over SHA-256) with the public key built into the launcher.
   Nothing is read from the list before the signature checks out.
2. Works out which files the game folder should hold, including the optional parts
   (`hd-textures`, `voice`) switched on in Settings.
3. Downloads what is missing or different from `objects/<sha256>` (resumable, two at a time,
   retried on network errors), checks every file's SHA-256 and size, and installs it.
   `config` files (`fpscap.ini`) are only installed when missing, so your edits stay.
   Files the list deletes are removed, and so are beta-only files when you leave the beta branch.

It keeps the launcher's own bookkeeping in `<game folder>/.psclassic/`
(`version.txt`, the `state.txt` hash cache, `beta-files.txt`, `download/`, `backup/`) in the
same format, so the Windows launcher sees the same install.

## Build

Go 1.26 or newer. The window uses [Fyne](https://fyne.io), which looks the same on
GNOME, KDE, other Linux desktops and macOS, and needs cgo with the X11, Wayland and
OpenGL headers. `scripts/build.sh` builds it in a Debian container (podman or docker),
so nothing extra is needed on the machine. `psclassic-updater` is built without cgo.

```sh
scripts/build.sh      # dist/psclassic-launcher and dist/psclassic-updater
scripts/install.sh    # into ~/.local/bin, with a menu entry and icon
scripts/check.sh      # gofmt, go vet, go test and govulncheck
```

With the headers installed (`libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev`
on Debian/Ubuntu), `NATIVE=1 scripts/build.sh` builds without a container.

### AppImage

```sh
scripts/appimage.sh   # dist/psclassic-launcher-<version>-x86_64.AppImage
```

The AppImage is built in an Ubuntu 22.04 container, so it runs on any distro with
glibc 2.35 or newer (Ubuntu 22.04+, Debian 12+, current Fedora and Arch, SteamOS). It
links only libGL and libwayland-client, which every desktop has, and uses the static
AppImage runtime, so the host needs no libfuse2. `appimagetool` and the runtime are
pinned by version and SHA-256. The AppImage works like the installed program: no
arguments opens the window, a command works in the terminal
(`./psclassic-launcher-*.AppImage play`).

### Steam Linux Runtime build

```sh
scripts/steamrt.sh    # dist/psclassic-launcher-<version>-steamrt4-x86_64.tar.gz
```

Builds both programs in Valve's Steam Runtime 4 SDK
(`registry.gitlab.steamos.cloud/steamrt/steamrt4/sdk`, pinned by version and digest in
`scripts/Containerfile.steamrt`), so the launcher uses only libraries the Steam Linux
Runtime 4.0 container has. The archive holds both programs, the menu entry, the icon,
this README and the license. To run the launcher inside the runtime's container, use the
runtime's `run` script. Heroic and Lutris (through umu) keep a copy in
`~/.local/share/umu/steamrt4`:

```sh
cd psclassic-launcher-<version>-steamrt4-x86_64
~/.local/share/umu/steamrt4/run -- ./psclassic-launcher
```

## Contributing

`main` is protected: every change goes on a branch and through a pull request, and
the **Check** workflow (gofmt, go vet, go test, govulncheck) has to pass first. To get
a clear message instead of a rejected push, turn on the repo's pre-push hook once per
clone:

```sh
git config core.hooksPath .githooks
```

## License

GPL-3.0. See `LICENSE`.
