import Foundation

// MARK: - Request (Go -> Helper)

struct Request: Decodable {
    let cmd: String
    let id: Int

    // install
    var ipsw: String?
    var disk: String?
    var disk_size_gb: Int?
    var cpus: Int?
    var memory_gb: Int?
    var out_dir: String?

    // guest provisioning (macOS 27+)
    var guest_user: String?
    var guest_password: String?
    var enable_ssh: Bool?

    // send_keys
    var keys: [String]?

    // click
    var x: Int?
    var y: Int?

    // type
    var text: String?
}

// MARK: - Message (Helper -> Go)

struct Message: Encodable {
    // Response fields
    var id: Int?
    var ok: Bool?
    var error: String?

    // Screenshot
    var width: Int?
    var height: Int?
    var png_base64: String?

    // VNC / network
    var vnc_port: Int?
    var mac_address: String?

    // Event fields
    var event: String?
    var progress: Int?
    var uom: String?
    var msg: String?
    var level: String?
    var exit_code: Int?

    static func response(id: Int, ok: Bool = true) -> Message {
        Message(id: id, ok: ok)
    }

    static func error(id: Int, message: String) -> Message {
        Message(id: id, error: message)
    }

    static func progress(_ pct: Int, msg: String) -> Message {
        Message(event: "progress", progress: pct, uom: "pct", msg: msg)
    }

    static func booted(vncPort: Int, macAddress: String? = nil) -> Message {
        Message(vnc_port: vncPort, mac_address: macAddress, event: "booted")
    }

    static func stopped(exitCode: Int = 0) -> Message {
        Message(event: "stopped", exit_code: exitCode)
    }

    static func screenshot(id: Int, width: Int, height: Int, pngBase64: String) -> Message {
        Message(id: id, ok: true, width: width, height: height, png_base64: pngBase64)
    }
}

// MARK: - JSON IO

let encoder: JSONEncoder = {
    let e = JSONEncoder()
    e.outputFormatting = [] // compact, one line
    return e
}()

let decoder = JSONDecoder()

func readRequest(from line: String) -> Request? {
    guard let data = line.data(using: .utf8) else {
        log("readRequest: failed to decode UTF-8, length=\(line.count)")
        return nil
    }
    guard let req = try? decoder.decode(Request.self, from: data) else {
        log("readRequest: JSON decode failed, bytes=\(data.count)")
        return nil
    }
    return req
}

private let writeLock = NSLock()

func writeMessage(_ msg: Message) {
    guard let data = try? encoder.encode(msg) else {
        log("writeMessage: JSON encode failed")
        return
    }
    let line = String(data: data, encoding: .utf8)! + "\n"
    writeLock.lock()
    FileHandle.standardOutput.write(line.data(using: .utf8)!)
    fflush(stdout)
    writeLock.unlock()
}

func writeError(id: Int, _ message: String) {
    log("writeError: id=\(id) \(message)")
    writeMessage(.error(id: id, message: message))
}

func log(_ message: String) {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
}
