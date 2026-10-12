MonitorSwap
===========

Press one key on either PC and the shared monitor moves to the other PC:
the PC that has it switches to "PC screen only", the other switches to
"Extend". It does the same thing as Win+P, on both PCs at once.


SETUP (about 2 minutes)

1. Desktop: copy the "Desktop" folder somewhere permanent
   (for example Documents\MonitorSwap) and double-click MonitorSwap.exe.
   Windows Firewall will ask once - click "Allow" (Private networks).

2. Laptop: copy the "Laptop" folder somewhere permanent and double-click
   MonitorSwap.exe. No firewall prompt and no admin rights are needed:
   the laptop only makes an outgoing connection to the desktop.

3. A small monitor icon appears near the clock on both PCs (it may be
   hidden under the ^ arrow). Click it: the second line should say
   "Other PC: connected".

4. Press Pause. Press it again to swap back.

Windows may show a blue "Windows protected your PC" box the first time,
because the program is not signed: click "More info" > "Run anyway".


USING IT

  Pause key ............. swap (works on either PC)
  Tray icon > menu ...... Swap now / Use shared monitor on this PC /
                          Send shared monitor to the other PC
  Command line .......... MonitorSwap.exe swap
                          MonitorSwap.exe take
                          MonitorSwap.exe give

  Blue filled icon  = the shared monitor is on this PC
  Grey outline icon = this PC has released it

If the other PC is off, asleep or unreachable, the PC where you pressed
the key still switches by itself.

Both copies start automatically when you sign in to Windows. Untick
"Start with Windows" in the tray menu to stop that.


SETTINGS (settings.json, next to the program)

Tray icon > "Edit settings...", save, then tray icon > "Restart".

  hotkey ............. "Pause", "Ctrl+Alt+M", "Win+Shift+F12", "ScrollLock"...
                       Each PC has its own, so the laptop can use a different
                       key if its keyboard has no Pause key.
  desktop_address .... Laptop only. Leave empty to find the desktop
                       automatically. If that fails, type the desktop's name
                       or IP here (shown as "This PC: ..." at the bottom of
                       the desktop's tray menu).
  key ................ Shared password between the two PCs. Type the same
                       text on both (anything you like). Empty works, but
                       then any device on your network could trigger a swap.
  port ............... Must be identical on both PCs. No need to change.
  role ............... "desktop" (waits for the connection) or "laptop"
                       (connects out). Do not change.


IF SOMETHING DOES NOT WORK

  Laptop says "Other PC: looking for the desktop..."
    - Is MonitorSwap running on the desktop, and did you click "Allow" on
      its firewall prompt? (If you missed it: Windows Security > Firewall >
      Allow an app through firewall > tick MonitorSwap for your network type.)
    - Put the desktop's name or IP in the laptop's "desktop_address".
    - A work VPN on the laptop can block access to your home network while
      it is connected.

  "Hotkey not available"
    - Another program owns that key. Choose a different one in settings.json.

  The laptop has no Pause key
    - Set a different "hotkey" in the laptop's settings.json.

  The monitor does not follow
    - Use "Open log" in the tray menu on both PCs; it records every step.

  The work laptop refuses to run the program
    - Company policy can block unknown programs. That is the company's
      setting, and it cannot be worked around from here.


UNINSTALL

Tray icon > untick "Start with Windows", then Exit, then delete the folder.

Source code: https://github.com/josedaidone/monitorswap
