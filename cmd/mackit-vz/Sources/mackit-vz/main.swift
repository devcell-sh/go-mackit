import Foundation

let manager = VMManager()

// Read JSONL from stdin on a background thread so the main run loop
// stays free for VZ callbacks (install progress, KVO, completion handlers).
DispatchQueue.global(qos: .userInitiated).async {
    while let line = readLine(strippingNewline: true) {
        guard !line.isEmpty else { continue }
        guard let req = readRequest(from: line) else {
            log("bad request: \(line)")
            continue
        }

        log("received command: \(req.cmd) id=\(req.id)")
        switch req.cmd {
        case "install":
            manager.install(req: req)
        case "boot":
            manager.boot(req: req)
        case "screenshot":
            manager.screenshot(req: req)
        case "send_keys":
            manager.sendKeys(req: req)
        case "click":
            manager.click(req: req)
        case "type":
            manager.typeText(req: req)
        case "stop":
            manager.stop(req: req)
            log("exiting after stop")
            exit(0)
        default:
            writeError(id: req.id, "unknown command: \(req.cmd)")
        }
        log("command completed: \(req.cmd) id=\(req.id)")
    }
    exit(0)
}

// Keep the main run loop alive for Virtualization.framework.
dispatchMain()
