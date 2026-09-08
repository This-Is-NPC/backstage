// Package guest drives a libvirt guest running Omarchy as a Backstage stage.
//
// **The guest is always Omarchy, and that is a guarantee rather than a
// preference.** Everything below leans on it: `pacman` with the `omarchy`
// repository to install the two tools by name; Hyprland's wlr-screencopy, which
// is what lets a machine with no GPU record its own screen at all; one seat,
// `seat0`, which is where a virtual keyboard has to appear; and the shell's own
// verbs for notifications, idle and restart. A guest that is not Omarchy has
// none of those in the same places, so `AssertOmarchy` refuses it by name
// before anything is installed or typed.
//
// That refusal is the point. Every failure in this package looks like a
// different failure: a locked session reads as a dead keyboard, a blanked
// screen reads as a crashed compositor, a missing ydotool socket reads as the
// wrong keystroke. Finding out three steps later that the machine was never
// Omarchy is the most expensive version of all of them.
package guest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The socket the ydotool daemon listens on, named rather than left to default.
//
// Not a flourish. Left alone, the daemon puts its socket in /tmp with mode 0600
// and a client under sudo looks somewhere else -- and the failure is `ydotool`
// exiting 0 with nothing happening on screen, which reads as the keystroke
// being wrong rather than the path being missing.
const socket = "/run/ydotoold.socket"

// What a stage needs on the far side, by name, out of Omarchy's own repository.
//
// `wf-recorder` and not `gpu-screen-recorder`: these guests have a `card0` and
// no render node, so MESA cannot find a driver -- `ZINK: failed to choose pdev`
// -- and the hardware recorder writes no file at all. wf-recorder goes through
// wlr-screencopy and encodes in software, which is slower and works.
var tools = []string{"ydotool", "wf-recorder"}

// Guest is one Omarchy computer: a libvirt domain, an account on it, and the
// ssh key that opens it.
type Guest struct {
	Domain string
	// User is the account whose session is filmed.
	User string
	// Admin is the account ssh connects as. It needs a key and sudo; the
	// filmed account needs neither, and usually should have neither.
	//
	// Two fields because they are two people. The point of filming a
	// household is that whoever is at the keyboard has no privilege --
	// giving the filmed account a key and passwordless sudo so a recorder
	// could reach it would be filming a different machine than the one
	// being described.
	Admin   string
	KeyFile string
	URI     string
	// Open is the program left on the desktop; empty means a terminal.
	Open string
	// Language is the locale that program runs under, so the film speaks
	// one language. Empty leaves the guest's own.
	Language string

	// Filled by Start.
	Address string
	uid     int
}

// New returns a guest with the defaults filled in.
func New(domain, user, admin, key, uri string) *Guest {
	if uri == "" {
		uri = "qemu:///system"
	}
	if admin == "" {
		admin = user
	}
	if key != "" && strings.HasPrefix(key, "~/") {
		key = filepath.Join(os.Getenv("HOME"), key[2:])
	}
	return &Guest{Domain: domain, User: user, Admin: admin, KeyFile: key, URI: uri}
}

func (g *Guest) virsh(args ...string) (string, error) {
	out, err := exec.Command("virsh", append([]string{"-c", g.URI}, args...)...).Output()
	return string(out), err
}

// ssh options that make a disposable guest reachable without an operator
// answering a host-key prompt the first time every clone is booted.
func (g *Guest) sshArgs(command string) []string {
	return []string{
		"-i", g.KeyFile,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		fmt.Sprintf("%s@%s", g.Admin, g.Address),
		command,
	}
}

// SSH runs a command as the operator and returns its combined output.
func (g *Guest) SSH(command string) (string, error) {
	out, err := exec.Command("ssh", g.sshArgs(command)...).CombinedOutput()
	return string(out), err
}

// Root runs a command under sudo on the guest.
func (g *Guest) Root(command string) (string, error) {
	return g.SSH("sudo " + command)
}

// Try runs a command and reports only whether it succeeded, for the many steps
// here whose failure is not a reason to stop: a card that was not there to
// dismiss, a service that was already down.
func (g *Guest) Try(command string) bool {
	_, err := g.SSH(command)
	return err == nil
}

// Fetch copies a file off the guest.
func (g *Guest) Fetch(remote, local string) error {
	// One `install` and not `cp && chmod`: `sudo a && b` puts only `a` under
	// sudo, so the mode change ran as the operator against a file owned by the
	// account being filmed and the copy failed at the last step of a take.
	//
	// Owned by the ssh account too, because scp reads it as that account and a
	// clip it cannot open is a take that was made and cannot be collected.
	staging := "/tmp/backstage-fetch"
	if out, err := g.Root(fmt.Sprintf("install -m 644 -o %s %s %s", g.Admin, remote, staging)); err != nil {
		return fmt.Errorf("%s: %s", remote, strings.TrimSpace(out))
	}
	args := []string{
		"-i", g.KeyFile,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		fmt.Sprintf("%s@%s:%s", g.Admin, g.Address, staging),
		local,
	}
	if out, err := exec.Command("scp", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("scp %s: %s", remote, strings.TrimSpace(string(out)))
	}
	_, _ = g.Root("rm -f " + staging)
	return nil
}

var address = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3})\b`)

// Start boots the domain if it is down and waits until ssh answers.
//
// Both waits are here rather than in the caller because they fail differently
// and a caller that conflated them would report "the machine never came up" for
// a machine that came up and has no network.
func (g *Guest) Start(patience time.Duration) error {
	state, _ := g.virsh("domstate", g.Domain)
	if !strings.Contains(state, "running") {
		if out, err := g.virsh("start", g.Domain); err != nil {
			return fmt.Errorf("starting %s: %s", g.Domain, strings.TrimSpace(out))
		}
	}
	deadline := time.Now().Add(patience)
	for time.Now().Before(deadline) {
		if out, err := g.virsh("domifaddr", g.Domain); err == nil {
			// The guest's own address and never the host's: a domain that has
			// not leased yet answers with a table and no address at all.
			if found := address.FindString(out); found != "" && !strings.HasPrefix(found, "127.") {
				g.Address = found
				break
			}
		}
		time.Sleep(3 * time.Second)
	}
	if g.Address == "" {
		return fmt.Errorf("%s never took an address", g.Domain)
	}
	for time.Now().Before(deadline) {
		if g.Try("true") {
			return g.readUID()
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("%s is at %s and never answered ssh", g.Domain, g.Address)
}

func (g *Guest) readUID() error {
	out, err := g.SSH("id -u " + g.User)
	if err != nil {
		return fmt.Errorf("reading the uid of %s: %s", g.User, strings.TrimSpace(out))
	}
	uid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return fmt.Errorf("the uid of %s is %q", g.User, strings.TrimSpace(out))
	}
	g.uid = uid
	return nil
}

// Session is the environment a command needs to reach this account's own
// Wayland session from an ssh that is not in it.
func (g *Guest) Session() string {
	// The compositor's instance too, and it is not optional. `hyprctl` without
	// it finds no instance and prints nothing, and a caller that parses the
	// nothing gets an empty answer instead of an error -- which is how a stage
	// that meant to close every window quietly closed none of them, twice.
	//
	// Newest first, because a session that was restarted leaves the old
	// signature's directory behind.
	return fmt.Sprintf("export XDG_RUNTIME_DIR=/run/user/%d WAYLAND_DISPLAY=wayland-1 "+
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%d/bus; "+
		"export HYPRLAND_INSTANCE_SIGNATURE=$(ls -t /run/user/%d/hypr 2>/dev/null | head -1)",
		g.uid, g.uid, g.uid)
}

// InSession runs a command inside the account's own session.
func (g *Guest) InSession(command string) (string, error) {
	// As the filmed account, through sudo, because the ssh belongs to the admin
	// and the session does not.
	quoted := strings.ReplaceAll(command, "'", `'\''`)
	return g.Root(fmt.Sprintf("-u %s bash -lc '%s; %s'", g.User, g.Session(), quoted))
}

// AssertOmarchy refuses a guest this package cannot drive.
//
// Named rather than inferred, and checked before anything is installed: every
// other failure in this package is ambiguous, and "the machine is not Omarchy"
// is the one that explains all of them at once.
func (g *Guest) AssertOmarchy() (version string, err error) {
	out, err := g.SSH("pacman -Q omarchy")
	if err != nil {
		return "", fmt.Errorf("%s is not an Omarchy machine (pacman -Q omarchy: %s). "+
			"The vm stage needs Omarchy's repository, its Hyprland and its seat; "+
			"nothing below would work here and most of it would fail as something else",
			g.Domain, strings.TrimSpace(out))
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return "", fmt.Errorf("%s answered %q for its Omarchy version", g.Domain, strings.TrimSpace(out))
	}
	return fields[1], nil
}

// Provision installs the tools and puts a keyboard on the seat. Idempotent.
func (g *Guest) Provision() error {
	if out, err := g.Root("pacman -S --noconfirm --needed " + strings.Join(tools, " ")); err != nil {
		return fmt.Errorf("installing %v on %s: %s", tools, g.Domain, tail(out))
	}
	if !g.Try("pgrep -x ydotoold") {
		_, _ = g.Root(fmt.Sprintf(
			"systemd-run --unit=ydotoold --description='ydotool (backstage)' "+
				"/usr/bin/ydotoold --socket-path=%s", socket))
	}
	// The device really on the seat, and not merely a daemon that started. This
	// is the check that turns a silent, expensive failure into a refusal: with
	// no device, every later keystroke is accepted and does nothing.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if out, _ := g.Root("loginctl seat-status seat0"); strings.Contains(out, "ydotoold virtual device") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("no virtual keyboard appeared on seat0 of %s", g.Domain)
}

func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}

// Prepare makes the desktop filmable, in an order where each step is the reason
// the next one could work.
//
//  1. a password, because `useradd -m` leaves none and a lock with no password
//     cannot be answered at all -- and Omarchy quite rightly refuses to restart
//     its own shell while the session is locked, so there is no way round it;
//  2. the idle timers, before they can fire. Omarchy blanks at 150s and locks
//     at 300s out of the box, and a scene that waits two minutes on one install
//     comes back to a dark screen and a password field;
//  3. a mouse move, because a *key* does not wake a blanked display. Measured:
//     with `dpms: Off`, ydotool reaches the seat and changes nothing;
//  4. the password typed, in case the lock was already up when this began;
//  5. the shell restarted, which is what makes the new timers current, and
//     which only works now that step 4 has cleared the lock;
//  6. the first-run cards dismissed. They are sent `-u critical`, so they never
//     expire and would sit in the corner for the whole film.
func (g *Guest) Prepare() error {
	_, _ = g.Root(fmt.Sprintf("sh -c 'echo %s:%s | chpasswd'", g.User, g.User))

	_, _ = g.Root(fmt.Sprintf("-u %s python3 -c 'import json,pathlib;"+
		"p=pathlib.Path(\"/home/%s/.config/omarchy/shell.json\");"+
		"d=json.loads(p.read_text());"+
		"d.setdefault(\"idle\",{}).update(screensaver=86400,lock=86400);"+
		"p.write_text(json.dumps(d,indent=2))'", g.User, g.User))

	_, _ = g.Root("YDOTOOL_SOCKET=" + socket + " ydotool mousemove -x 40 -y 40")
	time.Sleep(2 * time.Second)
	_, _ = g.Root(fmt.Sprintf("YDOTOOL_SOCKET=%s ydotool type --key-delay 20 '%s'", socket, g.User))
	_, _ = g.Root("YDOTOOL_SOCKET=" + socket + " ydotool key 28:1 28:0")
	time.Sleep(3 * time.Second)

	_, _ = g.InSession("omarchy restart shell")
	time.Sleep(3 * time.Second)
	// By summary, and the list is what Omarchy actually sends: the first-run
	// cards, and the failure cards a previous take left behind. A card from a
	// run that failed is the worst of them, because it is on screen describing
	// a problem that has since been fixed.
	for _, card := range []string{
		"Learn Keybindings", "Update System", "Wi-Fi",
		"Error", "App failure", "failed",
	} {
		_, _ = g.InSession(fmt.Sprintf("omarchy notification dismiss %q", card))
	}
	return nil
}

// ClearTheDesktop closes every window the filmed account has open.
//
// By asking the compositor what is there, rather than by killing the programs
// this package happens to know the names of. A stage that only closed its own
// terminal left a window from an earlier take tiled beside the new one, and the
// film showed two things at once with no explanation for either. That leftover
// had been opened by nothing in this package, so no list of names would ever
// have caught it.
//
// A take begins on an empty desktop. That is the whole rule.
func (g *Guest) ClearTheDesktop() error {
	out, err := g.InSession("hyprctl -j clients")
	if err == nil {
		var clients []struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal([]byte(out), &clients) == nil {
			for _, client := range clients {
				if client.PID > 0 {
					_, _ = g.Root(fmt.Sprintf("kill %d", client.PID))
				}
			}
		}
	}
	time.Sleep(2 * time.Second)
	return nil
}

// Open is the program the stage leaves on the desktop. Empty means a terminal.
//
// A scene drives what is in front of it, so this is what decides whether the
// film shows a shell or the product's own window.
//
// Set by the stage from the project config.

// OpenTerminal leaves exactly one program on the desktop, ready to be driven.
//
// Through `systemd-run --user` and not through Omarchy's SUPER+RETURN. A
// binding is what a person presses and it is not what a stage should depend on:
// the same keystroke opened a terminal on one guest and nothing on another with
// the same 225 bindings loaded and no config errors on either. The user manager
// also keeps the terminal alive after the ssh that asked for it has gone, which
// a backgrounded launch does not.
func (g *Guest) OpenTerminal() error {
	program, launch := "foot", "uwsm-app -- xdg-terminal-exec"
	if g.Open != "" {
		program, launch = g.Open, "uwsm-app -- "+g.Open
	}
	// One language on screen. The guest speaks whatever Omarchy was installed
	// with, and a film whose captions are in one language and whose pacman is
	// in another reads as two recordings spliced together.
	if g.Language != "" {
		launch = "systemd-run --user --collect --unit=backstage-open " +
			"--setenv=LANG=" + g.Language + " --setenv=LC_ALL=" + g.Language + " " + launch
	} else {
		launch = "systemd-run --user --collect --unit=backstage-open " + launch
	}
	if err := g.ClearTheDesktop(); err != nil {
		return err
	}
	// And the unit from the last take. `systemd-run` refuses a name that is
	// still loaded, and a take that ended with its window open leaves one --
	// so the second scene of a production failed to open anything at all, with
	// an exit status and no reason attached to it.
	_, _ = g.InSession("systemctl --user stop backstage-open")
	_, _ = g.InSession("systemctl --user reset-failed backstage-open")
	time.Sleep(time.Second)
	if _, err := g.InSession(launch); err != nil {
		return fmt.Errorf("opening %s on %s: %w", program, g.Domain, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if g.Try("pgrep -u " + g.User + " -x " + program) {
			// Open is not the same as drawn. A window that has mapped but not
			// painted is a first step typed into nothing, and the failure looks
			// like the keyboard.
			time.Sleep(2 * time.Second)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s never came up on %s, so a scene would have nothing to drive",
		program, g.Domain)
}

// Type sends literal text to whatever the guest's compositor is pointing at.
func (g *Guest) Type(text string, keyDelay time.Duration) error {
	quoted := strings.ReplaceAll(text, "'", `'\''`)
	ms := int(keyDelay / time.Millisecond)
	if ms <= 0 {
		ms = 14
	}
	out, err := g.Root(fmt.Sprintf("YDOTOOL_SOCKET=%s ydotool type --key-delay %d '%s'",
		socket, ms, quoted))
	if err != nil {
		return fmt.Errorf("typing at %s: %s", g.Domain, tail(out))
	}
	return nil
}

// Codes are the evdev key numbers a scene's named keys resolve to. Written out
// rather than computed, because a wrong number is a keystroke that silently
// does something else.
var Codes = map[string]string{
	"enter": "28", "return": "28", "esc": "1", "escape": "1", "tab": "15",
	"space": "57", "backspace": "14", "delete": "111",
	"up": "103", "down": "108", "left": "105", "right": "106",
	"home": "102", "end": "107", "pgup": "104", "pgdn": "109",
	"super": "125", "ctrl": "29", "alt": "56", "shift": "42",
}

// Key presses one named key, or a chord written `super+return`.
func (g *Guest) Key(name string) error {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(name)), "+")
	var down, up []string
	for _, part := range parts {
		code, ok := Codes[part]
		if !ok {
			return fmt.Errorf("no key named %q; the vm stage knows %d names", part, len(Codes))
		}
		down = append(down, code+":1")
		up = append([]string{code + ":0"}, up...)
	}
	out, err := g.Root(fmt.Sprintf("YDOTOOL_SOCKET=%s ydotool key %s",
		socket, strings.Join(append(down, up...), " ")))
	if err != nil {
		return fmt.Errorf("pressing %s at %s: %s", name, g.Domain, tail(out))
	}
	return nil
}

// Facts records what a take was made against, beside the clip.
//
// A film is evidence and evidence has a provenance. `pacman -Q omarchy` today
// and `pacman -Q omarchy` in six months are the difference between a take that
// can be reproduced and one that can only be re-shot.
type Facts struct {
	Domain  string `json:"domain"`
	User    string `json:"user"`
	Omarchy string `json:"omarchy"`
	Address string `json:"address"`
	Made    string `json:"made"`
}

// WriteFacts drops the sidecar beside a clip.
func (g *Guest) WriteFacts(clip, version string) error {
	facts := Facts{
		Domain: g.Domain, User: g.User, Omarchy: version,
		Address: g.Address, Made: time.Now().Format(time.RFC3339),
	}
	body, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(clip, filepath.Ext(clip))+".facts.json",
		append(body, '\n'), 0o644)
}
