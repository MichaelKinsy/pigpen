// type: types text or presses keys in the focused window via real keyboard events.
//   type "text"
//   type --key return|escape|tab|backspace|down|up|left|right|<letter> [count] [cmd,shift,alt,ctrl]
import CoreGraphics
import Foundation

let codes: [String: CGKeyCode] = [
  "return": 36, "backspace": 51, "escape": 53, "tab": 48, "space": 49, "down": 125, "up": 126, "left": 123, "right": 124,
  "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
  "y": 16, "t": 17, "o": 31, "u": 32, "i": 34, "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46, "/": 44,
]

func post(_ code: CGKeyCode, _ text: String? = nil, _ flags: CGEventFlags = []) {
  for down in [true, false] {
    let e = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: down)!
    if let t = text { let u = Array(t.utf16); e.keyboardSetUnicodeString(stringLength: u.count, unicodeString: u) }
    if !flags.isEmpty { e.flags = flags }
    e.post(tap: .cghidEventTap)
    usleep(8_000)
  }
}

let args = CommandLine.arguments
if args.count >= 3 && args[1] == "--key" {
  guard let c = codes[args[2]] else { exit(2) }
  let n = args.count > 3 ? Int(args[3]) ?? 1 : 1
  var flags: CGEventFlags = []
  if args.count > 4 {
    for m in args[4].split(separator: ",") {
      switch m {
      case "cmd": flags.insert(.maskCommand)
      case "shift": flags.insert(.maskShift)
      case "alt": flags.insert(.maskAlternate)
      case "ctrl": flags.insert(.maskControl)
      default: break
      }
    }
  }
  for _ in 0..<n { post(c, nil, flags) }
} else if args.count >= 2 {
  for ch in args[1] { post(codes[String(ch).lowercased()] ?? 0, String(ch)) }
}
