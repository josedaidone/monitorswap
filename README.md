# MonitorSwap

One hotkey to hand a shared monitor back and forth between two Windows PCs.

```
 Laptop  (its own screen) ───┐
                             ├─── shared monitor
 Desktop (its own monitor) ──┘
```

## Why

I have a laptop and a desktop on the same desk. Each has its own screen, and
both are also plugged into one extra monitor. Every time I wanted that monitor
on the other computer, I had to go to both machines and change Win+P by hand:
"PC screen only" on one, "Extend" on the other.

MonitorSwap does both at once. Press a key on either PC: the one using the
shared monitor lets go, the other one takes it, and the monitor follows.

## How it works

- A small tray app runs on each PC. The two talk over your local network.
- When you press the hotkey, that PC looks at its own state, decides for both,
  and tells the other one exactly what to do ("you extend" / "you release").
  Because one side decides, the two can't end up both extended or both released.
- Each PC then switches projection mode with `SetDisplayConfig`, the same
  Windows API behind Win+P.
- The monitor does the rest: when one PC stops sending a signal, it switches to
  the input that still has one.
- If the other PC is off, asleep or unreachable, the PC where you pressed the
  key still switches by itself.

The **desktop** copy listens for a connection; the **laptop** copy only connects
out. That means the laptop needs no firewall change and no admin rights, which
helps if it is a locked-down work machine.

## Requirements

- Windows 10 or 11 on both PCs, on the same local network.
- A monitor that **switches input automatically** when the active signal goes
  away. MonitorSwap changes what each PC outputs; it does not change the
  monitor's input.
- On each PC, Win+P > "PC screen only" must leave the screen you want to keep.
  Try it by hand once before installing.

## Install

1. Download `MonitorSwap.zip` from the
   [Releases](https://github.com/josedaidone/monitorswap/releases) page and unzip it.
2. Open `Desktop\settings.json` and `Laptop\settings.json` and type the same
   text in `"key"` on both (any password you like).
3. **Desktop:** copy the `Desktop` folder somewhere permanent and run
   `MonitorSwap.exe`. Click **Allow** on the one-time Windows Firewall prompt.
4. **Laptop:** copy the `Laptop` folder somewhere permanent and run
   `MonitorSwap.exe`.
5. Click the monitor icon near the clock. It should say "Other PC: connected".
6. Press **Pause**. Press it again to swap back.

The exe is not code-signed, so Windows may show "Windows protected your PC" the
first time: **More info > Run anyway**. If you would rather not run a downloaded
binary, [build it yourself](#build-from-source).

"Desktop" and "laptop" are just names for the two roles. Any two Windows PCs
work; use the `Desktop` folder on the one where you can accept a firewall prompt.

## Use

| Action | How |
| --- | --- |
| Swap | Press the hotkey (default **Pause**) on either PC |
| Take or give explicitly | Tray icon > "Use shared monitor on this PC" / "Send shared monitor to the other PC" |
| From a script | `MonitorSwap.exe swap`, `MonitorSwap.exe take`, `MonitorSwap.exe give` |

A solid blue tray icon means the shared monitor is on this PC. A grey outline
means this PC has released it.

Both copies start with Windows. Untick "Start with Windows" in the tray menu to
stop that.

## Settings

`settings.json` sits next to the exe. Tray icon > "Edit settings...", save, then
tray icon > "Restart".

| Setting | Meaning |
| --- | --- |
| `role` | `"desktop"` (waits for the connection) or `"laptop"` (connects out) |
| `hotkey` | For example `"Pause"`, `"Ctrl+Alt+M"`, `"Win+Shift+F12"`, `"ScrollLock"`. Each PC has its own, so a laptop without a Pause key can use something else |
| `desktop_address` | Laptop only. Empty finds the desktop automatically. If that fails, put the desktop's name or IP here (shown at the bottom of the desktop's tray menu) |
| `port` | TCP/UDP port the desktop listens on (default 47823). Must match on both |
| `key` | Shared password. Must match on both |
| `start_with_windows` | Launch at sign-in (per-user registry entry, no admin rights) |

## Troubleshooting

**Laptop says "Other PC: looking for the desktop..."**
Check that MonitorSwap is running on the desktop and was allowed through its
firewall (Windows Security > Firewall > Allow an app through firewall). If
automatic discovery is blocked, set `desktop_address` on the laptop. A VPN on
the laptop can also cut it off from the local network.

**"Hotkey not available"**
Another program owns that key. Pick a different `hotkey`.

**The monitor doesn't follow**
Use "Open log" in the tray menu on both PCs. `monitorswap.log` records every
step, including what Windows answered.

**A managed work PC refuses to run it**
Company policy can block unknown programs. MonitorSwap does not try to get
around that.

## Security notes

- The desktop accepts connections only from local-network addresses, and only
  from a peer that knows the `key`.
- The only thing a connected peer can ask for is "switch to Extend" or "switch
  to PC screen only".
- The connection is not encrypted. It is meant for a home or office LAN, not
  for use across the internet.

## Build from source

Needs Go 1.21 or newer. No external libraries.

```sh
./package.sh        # runs the tests, builds the exe, makes dist/MonitorSwap.zip
```

Or by hand, on Windows:

```bat
go test ./...
go build -trimpath -ldflags "-H=windowsgui -s -w" -o MonitorSwap.exe .
```

| File | What it holds |
| --- | --- |
| `main_windows.go` | Tray icon, menu, hotkey, startup entry, log |
| `display_windows.go` | Reading and switching the projection mode |
| `net.go` | Connection between the two PCs and discovery |
| `swap.go` | The decision logic for a swap |
| `config.go`, `hotkey.go` | `settings.json` and hotkey parsing |

## License

[MIT](LICENSE)
