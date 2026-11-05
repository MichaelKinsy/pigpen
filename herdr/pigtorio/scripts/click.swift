// click: posts a real mouse click to the sandbox Herdr window at a point
// relative to the window's top-left (logical px), without raising other apps.
//   click <pid> <x> <y> [left|right]
import CoreGraphics
import Foundation

let args = CommandLine.arguments
guard args.count >= 4, let pid = Int32(args[1]), let x = Double(args[2]), let y = Double(args[3]) else {
  FileHandle.standardError.write("usage: click <pid> <x> <y> [left|right]\n".data(using: .utf8)!)
  exit(2)
}
let right = args.count > 4 && args[4] == "right"
let list = CGWindowListCopyWindowInfo([.optionAll], kCGNullWindowID) as? [[String: Any]] ?? []
var origin: CGPoint? = nil
var best = 0.0
for w in list {
  guard (w[kCGWindowOwnerPID as String] as? Int32) == pid, (w[kCGWindowLayer as String] as? Int) == 0,
        let b = w[kCGWindowBounds as String] as? [String: Double] else { continue }
  let area = (b["Width"] ?? 0) * (b["Height"] ?? 0)
  if area > best { best = area; origin = CGPoint(x: b["X"] ?? 0, y: b["Y"] ?? 0) }
}
guard let o = origin else { FileHandle.standardError.write("no window for pid\n".data(using: .utf8)!); exit(1) }
let p = CGPoint(x: o.x + x, y: o.y + y)
let down: CGEventType = right ? .rightMouseDown : .leftMouseDown
let up: CGEventType = right ? .rightMouseUp : .leftMouseUp
let button: CGMouseButton = right ? .right : .left
let move = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved, mouseCursorPosition: p, mouseButton: button)!
move.post(tap: .cghidEventTap)
usleep(30_000)
let d = CGEvent(mouseEventSource: nil, mouseType: down, mouseCursorPosition: p, mouseButton: button)!
d.setIntegerValueField(.mouseEventClickState, value: 1)
d.post(tap: .cghidEventTap)
usleep(40_000)
let u = CGEvent(mouseEventSource: nil, mouseType: up, mouseCursorPosition: p, mouseButton: button)!
u.setIntegerValueField(.mouseEventClickState, value: 1)
u.post(tap: .cghidEventTap)
print("clicked \(Int(p.x)),\(Int(p.y))")
