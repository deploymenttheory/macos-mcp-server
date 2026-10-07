//go:build darwin && (amd64 || arm64)

package macdesktop

import "strings"

// Virtual key codes from Carbon's Events.h. They identify physical keys on the
// ANSI layout, independent of the current keyboard mapping, which is why
// shortcuts are sent through them while text is sent as Unicode.
const (
	vkA             uint16 = 0x00
	vkS             uint16 = 0x01
	vkD             uint16 = 0x02
	vkF             uint16 = 0x03
	vkH             uint16 = 0x04
	vkG             uint16 = 0x05
	vkZ             uint16 = 0x06
	vkX             uint16 = 0x07
	vkC             uint16 = 0x08
	vkV             uint16 = 0x09
	vkB             uint16 = 0x0B
	vkQ             uint16 = 0x0C
	vkW             uint16 = 0x0D
	vkE             uint16 = 0x0E
	vkR             uint16 = 0x0F
	vkY             uint16 = 0x10
	vkT             uint16 = 0x11
	vk1             uint16 = 0x12
	vk2             uint16 = 0x13
	vk3             uint16 = 0x14
	vk4             uint16 = 0x15
	vk6             uint16 = 0x16
	vk5             uint16 = 0x17
	vkEqual         uint16 = 0x18
	vk9             uint16 = 0x19
	vk7             uint16 = 0x1A
	vkMinus         uint16 = 0x1B
	vk8             uint16 = 0x1C
	vk0             uint16 = 0x1D
	vkRightBracket  uint16 = 0x1E
	vkO             uint16 = 0x1F
	vkU             uint16 = 0x20
	vkLeftBracket   uint16 = 0x21
	vkI             uint16 = 0x22
	vkP             uint16 = 0x23
	vkL             uint16 = 0x25
	vkJ             uint16 = 0x26
	vkQuote         uint16 = 0x27
	vkK             uint16 = 0x28
	vkSemicolon     uint16 = 0x29
	vkBackslash     uint16 = 0x2A
	vkComma         uint16 = 0x2B
	vkSlash         uint16 = 0x2C
	vkN             uint16 = 0x2D
	vkM             uint16 = 0x2E
	vkPeriod        uint16 = 0x2F
	vkGrave         uint16 = 0x32
	vkReturn        uint16 = 0x24
	vkTab           uint16 = 0x30
	vkSpace         uint16 = 0x31
	vkDelete        uint16 = 0x33 // backspace
	vkEscape        uint16 = 0x35
	vkCommand       uint16 = 0x37
	vkShift         uint16 = 0x38
	vkCapsLock      uint16 = 0x39
	vkOption        uint16 = 0x3A
	vkControl       uint16 = 0x3B
	vkRightCommand  uint16 = 0x36
	vkFunction      uint16 = 0x3F
	vkF17           uint16 = 0x40
	vkF18           uint16 = 0x4F
	vkF19           uint16 = 0x50
	vkF20           uint16 = 0x5A
	vkF5            uint16 = 0x60
	vkF6            uint16 = 0x61
	vkF7            uint16 = 0x62
	vkF3            uint16 = 0x63
	vkF8            uint16 = 0x64
	vkF9            uint16 = 0x65
	vkF11           uint16 = 0x67
	vkF13           uint16 = 0x69
	vkF16           uint16 = 0x6A
	vkF14           uint16 = 0x6B
	vkF10           uint16 = 0x6D
	vkF12           uint16 = 0x6F
	vkF15           uint16 = 0x71
	vkHelp          uint16 = 0x72
	vkHome          uint16 = 0x73
	vkPageUp        uint16 = 0x74
	vkForwardDelete uint16 = 0x75
	vkF4            uint16 = 0x76
	vkEnd           uint16 = 0x77
	vkF2            uint16 = 0x78
	vkPageDown      uint16 = 0x79
	vkF1            uint16 = 0x7A
	vkLeftArrow     uint16 = 0x7B
	vkRightArrow    uint16 = 0x7C
	vkDownArrow     uint16 = 0x7D
	vkUpArrow       uint16 = 0x7E
	vkKeypadEnter   uint16 = 0x4C
	vkVolumeUp      uint16 = 0x48
	vkVolumeDown    uint16 = 0x49
	vkMute          uint16 = 0x4A
)

// modifierKeys are the keys that set a flag rather than typing. "ctrl" is the
// Control key — literal, not Command — because a chord written for macOS
// means what it says; the alias table maps the Windows names onto Command.
var modifierKeys = map[string]uint16{
	"cmd": vkCommand, "command": vkCommand, "meta": vkCommand, "super": vkCommand,
	"win": vkCommand, "windows": vkCommand,
	"ctrl": vkControl, "control": vkControl,
	"alt": vkOption, "option": vkOption, "opt": vkOption,
	"shift": vkShift,
	"fn":    vkFunction,
}

// namedKeys maps key names to virtual key codes.
var namedKeys = map[string]uint16{
	"enter": vkReturn, "return": vkReturn,
	"tab":       vkTab,
	"space":     vkSpace,
	"esc":       vkEscape,
	"escape":    vkEscape,
	"backspace": vkDelete, "back": vkDelete,
	"delete": vkForwardDelete, "del": vkForwardDelete, "forwarddelete": vkForwardDelete,
	"home": vkHome, "end": vkEnd,
	"pageup": vkPageUp, "pgup": vkPageUp,
	"pagedown": vkPageDown, "pgdn": vkPageDown,
	"up": vkUpArrow, "down": vkDownArrow, "left": vkLeftArrow, "right": vkRightArrow,
	"capslock": vkCapsLock, "help": vkHelp,
	"f1": vkF1, "f2": vkF2, "f3": vkF3, "f4": vkF4, "f5": vkF5, "f6": vkF6,
	"f7": vkF7, "f8": vkF8, "f9": vkF9, "f10": vkF10, "f11": vkF11, "f12": vkF12,
	"f13": vkF13, "f14": vkF14, "f15": vkF15, "f16": vkF16, "f17": vkF17,
	"f18": vkF18, "f19": vkF19, "f20": vkF20,
	"volumeup": vkVolumeUp, "volumedown": vkVolumeDown, "mute": vkMute,
	"`": vkGrave, "-": vkMinus, "=": vkEqual, "[": vkLeftBracket, "]": vkRightBracket,
	"\\": vkBackslash, ";": vkSemicolon, "'": vkQuote, ",": vkComma, ".": vkPeriod, "/": vkSlash,
	"minus": vkMinus, "plus": vkEqual, "equal": vkEqual, "comma": vkComma, "period": vkPeriod,
	"slash": vkSlash, "backslash": vkBackslash, "semicolon": vkSemicolon, "quote": vkQuote,
	"grave": vkGrave, "backtick": vkGrave,
}

// letterKeys maps a-z to their ANSI virtual key codes.
var letterKeys = map[byte]uint16{
	'a': vkA, 'b': vkB, 'c': vkC, 'd': vkD, 'e': vkE, 'f': vkF, 'g': vkG, 'h': vkH, 'i': vkI,
	'j': vkJ, 'k': vkK, 'l': vkL, 'm': vkM, 'n': vkN, 'o': vkO, 'p': vkP, 'q': vkQ, 'r': vkR,
	's': vkS, 't': vkT, 'u': vkU, 'v': vkV, 'w': vkW, 'x': vkX, 'y': vkY, 'z': vkZ,
	'0': vk0, '1': vk1, '2': vk2, '3': vk3, '4': vk4, '5': vk5, '6': vk6, '7': vk7, '8': vk8, '9': vk9,
}

// keyNameToVK resolves a key name to a virtual key code and reports whether it
// is a modifier.
func keyNameToVK(name string) (vk uint16, modifier, ok bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if vk, ok := modifierKeys[n]; ok {
		return vk, true, true
	}
	if vk, ok := namedKeys[n]; ok {
		return vk, false, true
	}
	if len(n) == 1 {
		if vk, ok := letterKeys[n[0]]; ok {
			return vk, false, true
		}
	}
	return 0, false, false
}
