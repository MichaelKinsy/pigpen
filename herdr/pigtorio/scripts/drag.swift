// drag: presses at one point of the sandbox Herdr window, moves to another in
// steps, and releases (real HID events).  drag <pid> <x1> <y1> <x2> <y2>
import CoreGraphics
import Foundation

let a = CommandLine.arguments
guard a.count >= 6, let pid = Int32(a[1]), let x1 = Double(a[2]), let y1 = Double(a[3]), let x2 = Double(a[4]), let y2 = Double(a[5]) else { exit(2) }
let list = CGWindowListCopyWindowInfo([.optionAll], kCGNullWindowID) as? [[String: Any]] ?? []
var origin = CGPoint.zero
var best = 0.0
for w in list {
  guard (w[kCGWindowOwnerPID as String] as? Int32) == pid, (w[kCGWindowLayer as String] as? Int) == 0,
        let b = w[kCGWindowBounds as String] as? [String: Double] else { continue }
  let area = (b["Width"] ?? 0) * (b["Height"] ?? 0)
  if area > best { best = area; origin = CGPoint(x: b["X"] ?? 0, y: b["Y"] ?? 0) }
}
func at(_ x: Double, _ y: Double) -> CGPoint { CGPoint(x: origin.x + x, y: origin.y + y) }
CGEvent(mouseEventSource: nil, mouseType: .mouseMoved, mouseCursorPosition: at(x1, y1), mouseButton: .left)!.post(tap: .cghidEventTap)
usleep(40_000)
CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: at(x1, y1), mouseButton: .left)!.post(tap: .cghidEventTap)
let steps = 20
for i in 1...steps {
  usleep(25_000)
  let t = Double(i) / Double(steps)
  CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged, mouseCursorPosition: at(x1 + (x2 - x1) * t, y1 + (y2 - y1) * t), mouseButton: .left)!.post(tap: .cghidEventTap)
}
usleep(60_000)
CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: at(x2, y2), mouseButton: .left)!.post(tap: .cghidEventTap)
