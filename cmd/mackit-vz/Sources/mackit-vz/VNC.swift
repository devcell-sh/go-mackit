import Foundation
import Virtualization
import Dynamic
import CoreGraphics
import ImageIO
import CommonCrypto
import ObjCExceptionCatch

// VNCServer wraps _VZVNCServer via the Dynamic library (runtime ObjC dispatch).
// Same approach as tart (cirruslabs/tart): DispatchQueue.global() for the
// callback queue, password-based security configuration.
class VNCServer {
    private let server: Dynamic
    let password: String

    init?(virtualMachine: VZVirtualMachine) {
        log("VNC: init, thread=\(Thread.isMainThread ? "main" : "background")")
        let pw = VNCServer.randomPassword()
        var result: Dynamic?
        var exceptionMsg: NSString?
        let ok = ObjCTryCatch({
            result = VNCServer.createServer(vm: virtualMachine, password: pw)
        }, &exceptionMsg)
        if !ok {
            log("VNC: ObjC exception: \(exceptionMsg ?? "unknown")")
            return nil
        }
        guard let s = result else {
            log("VNC: createServer returned nil")
            return nil
        }
        server = s
        password = pw
    }

    private static func randomPassword() -> String {
        let chars = "abcdefghijklmnopqrstuvwxyz0123456789"
        return String((0..<8).map { _ in chars.randomElement()! })
    }

    private static func createServer(vm: VZVirtualMachine, password: String) -> Dynamic? {
        guard NSClassFromString("_VZVNCServer") != nil else {
            log("VNC: _VZVNCServer class not found")
            return nil
        }
        guard NSClassFromString("_VZVNCAuthenticationSecurityConfiguration") != nil else {
            log("VNC: _VZVNCAuthenticationSecurityConfiguration class not found")
            return nil
        }
        log("VNC: classes found, creating security config")

        let secConfig = Dynamic._VZVNCAuthenticationSecurityConfiguration(password: password)
        guard secConfig.asAnyObject != nil else {
            log("VNC: security config init returned nil")
            return nil
        }
        log("VNC: security config created")

        let s = Dynamic._VZVNCServer(
            port: 0,
            queue: DispatchQueue.global(),
            securityConfiguration: secConfig
        )
        guard s.asAnyObject != nil else {
            log("VNC: server init returned nil")
            return nil
        }
        log("VNC: server created, setting VM")

        s.virtualMachine = vm
        log("VNC: starting server")
        s.start()
        log("VNC: server started")
        return s
    }

    func waitForPort() -> Int? {
        for i in 0..<100 {
            let raw = server.port
            let asInt = raw.asInt
            if i < 3 || i % 20 == 0 {
                log("VNC: waitForPort[\(i)] asInt=\(asInt ?? -1) raw=\(raw)")
            }
            if let port = asInt, port > 0 {
                log("VNC: port=\(port) after \(i * 50)ms")
                return port
            }
            usleep(50_000)
        }
        log("VNC: port not assigned after 5s")
        return nil
    }

    func port() -> Int {
        return server.port.asInt ?? 0
    }

    func stop() {
        log("VNC: stopping")
        server.stop()
    }

    func captureScreenshot() -> Data? {
        let p = port()
        guard p > 0 else {
            log("VNC capture: no port")
            return nil
        }
        return RFBCapture.capture(port: p, password: password)
    }
}

// Minimal RFB 3.8 client for local framebuffer capture.
// Connects to the VNC server, grabs one raw frame, returns PNG data.
enum RFBCapture {
    static func capture(port: Int, password: String) -> Data? {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return nil }
        defer { Darwin.close(fd) }

        var tv = timeval(tv_sec: 5, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = UInt16(port).bigEndian
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")

        let connectOK = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard connectOK == 0 else {
            log("VNC capture: connect failed (errno=\(errno))")
            return nil
        }

        // Version handshake
        var verBuf = [UInt8](repeating: 0, count: 12)
        guard recvAll(fd, &verBuf, 12) else { return nil }
        var clientVer = Array("RFB 003.008\n".utf8)
        guard sendAll(fd, &clientVer, 12) else { return nil }

        // Security negotiation
        var numTypes: UInt8 = 0
        guard recvAll(fd, &numTypes, 1), numTypes > 0 else { return nil }
        var types = [UInt8](repeating: 0, count: Int(numTypes))
        guard recvAll(fd, &types, Int(numTypes)) else { return nil }

        if types.contains(2) {
            // VNC Authentication (type 2): DES challenge-response
            var chosenAuth: UInt8 = 2
            guard sendAll(fd, &chosenAuth, 1) else { return nil }
            var challenge = [UInt8](repeating: 0, count: 16)
            guard recvAll(fd, &challenge, 16) else { return nil }
            let response = vncDesEncrypt(challenge: challenge, password: password)
            var resp = response
            guard sendAll(fd, &resp, 16) else { return nil }
        } else if types.contains(1) {
            // None auth
            var chosenAuth: UInt8 = 1
            guard sendAll(fd, &chosenAuth, 1) else { return nil }
        } else {
            log("VNC capture: no supported auth (types: \(types))")
            return nil
        }
        var secResult = [UInt8](repeating: 0, count: 4)
        guard recvAll(fd, &secResult, 4) else { return nil }
        guard secResult == [0, 0, 0, 0] else { return nil }

        // ClientInit (shared=true)
        var shared: UInt8 = 1
        guard sendAll(fd, &shared, 1) else { return nil }

        // ServerInit: width(2) + height(2) + pixel_format(16) + name_len(4) + name
        var dims = [UInt8](repeating: 0, count: 4)
        guard recvAll(fd, &dims, 4) else { return nil }
        let w = Int(dims[0]) << 8 | Int(dims[1])
        let h = Int(dims[2]) << 8 | Int(dims[3])

        var pf = [UInt8](repeating: 0, count: 16)
        guard recvAll(fd, &pf, 16) else { return nil }

        var nlBuf = [UInt8](repeating: 0, count: 4)
        guard recvAll(fd, &nlBuf, 4) else { return nil }
        let nl = Int(nlBuf[0]) << 24 | Int(nlBuf[1]) << 16 | Int(nlBuf[2]) << 8 | Int(nlBuf[3])
        if nl > 0 && nl < 4096 {
            var name = [UInt8](repeating: 0, count: nl)
            guard recvAll(fd, &name, nl) else { return nil }
        }

        // SetPixelFormat: 32bpp, depth 24, little-endian, true-color, RGB shifts 16/8/0
        var spf: [UInt8] = [
            0, 0, 0, 0,           // type=SetPixelFormat + padding
            32, 24, 0, 1,         // bpp, depth, big-endian=no, true-color=yes
            0, 0xFF, 0, 0xFF, 0, 0xFF, // red/green/blue max = 255
            16, 8, 0,             // red/green/blue shift
            0, 0, 0               // padding
        ]
        guard sendAll(fd, &spf, 20) else { return nil }

        // SetEncodings: Raw only
        var enc: [UInt8] = [2, 0, 0, 1, 0, 0, 0, 0]
        guard sendAll(fd, &enc, 8) else { return nil }

        // FramebufferUpdateRequest (full, non-incremental)
        var fbReq: [UInt8] = [
            3, 0,
            0, 0, 0, 0,
            UInt8((w >> 8) & 0xFF), UInt8(w & 0xFF),
            UInt8((h >> 8) & 0xFF), UInt8(h & 0xFF),
        ]
        guard sendAll(fd, &fbReq, 10) else { return nil }

        // Read FramebufferUpdate response
        var fbuHeader = [UInt8](repeating: 0, count: 4)
        guard recvAll(fd, &fbuHeader, 4) else { return nil }

        var rectHeader = [UInt8](repeating: 0, count: 12)
        guard recvAll(fd, &rectHeader, 12) else { return nil }
        let encoding = Int32(rectHeader[8]) << 24 | Int32(rectHeader[9]) << 16 |
                        Int32(rectHeader[10]) << 8 | Int32(rectHeader[11])
        guard encoding == 0 else {
            log("VNC capture: unsupported encoding \(encoding)")
            return nil
        }

        let pixelCount = w * h * 4
        let pixels = UnsafeMutablePointer<UInt8>.allocate(capacity: pixelCount)
        defer { pixels.deallocate() }
        guard recvAll(fd, pixels, pixelCount) else { return nil }

        // Convert to PNG
        return pixelsToPNG(pixels: pixels, width: w, height: h)
    }

    private static func pixelsToPNG(pixels: UnsafeMutablePointer<UInt8>, width: Int, height: Int) -> Data? {
        let colorSpace = CGColorSpaceCreateDeviceRGB()
        let bitmapInfo = CGBitmapInfo(rawValue:
            CGImageAlphaInfo.noneSkipFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue)
        guard let ctx = CGContext(
            data: pixels,
            width: width, height: height,
            bitsPerComponent: 8, bytesPerRow: width * 4,
            space: colorSpace, bitmapInfo: bitmapInfo.rawValue
        ) else { return nil }

        guard let image = ctx.makeImage() else { return nil }
        let data = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(
            data as CFMutableData, "public.png" as CFString, 1, nil
        ) else { return nil }
        CGImageDestinationAddImage(dest, image, nil)
        guard CGImageDestinationFinalize(dest) else { return nil }
        return data as Data
    }

    // VNC DES authentication: encrypt 16-byte challenge with password.
    // VNC reverses each key byte's bits before DES-ECB encryption.
    private static func vncDesEncrypt(challenge: [UInt8], password: String) -> [UInt8] {
        var key = [UInt8](repeating: 0, count: 8)
        let pwBytes = Array(password.utf8)
        for i in 0..<min(8, pwBytes.count) {
            key[i] = reverseBits(pwBytes[i])
        }

        var result = [UInt8](repeating: 0, count: 16)
        var keyData = key
        var cryptor: CCCryptorRef?
        CCCryptorCreate(CCOperation(kCCEncrypt), CCAlgorithm(kCCAlgorithmDES),
                        CCOptions(kCCOptionECBMode), &keyData, 8, nil, &cryptor)
        if let c = cryptor {
            var moved: Int = 0
            var block0 = Array(challenge[0..<8])
            var block1 = Array(challenge[8..<16])
            result.withUnsafeMutableBufferPointer { buf in
                CCCryptorUpdate(c, &block0, 8, buf.baseAddress!, 8, &moved)
                CCCryptorUpdate(c, &block1, 8, buf.baseAddress! + 8, 8, &moved)
            }
            CCCryptorFinal(c, nil, 0, &moved)
            CCCryptorRelease(c)
        }
        return result
    }

    private static func reverseBits(_ b: UInt8) -> UInt8 {
        var v = b
        v = ((v & 0xF0) >> 4) | ((v & 0x0F) << 4)
        v = ((v & 0xCC) >> 2) | ((v & 0x33) << 2)
        v = ((v & 0xAA) >> 1) | ((v & 0x55) << 1)
        return v
    }

    private static func recvAll(_ fd: Int32, _ buf: UnsafeMutableRawPointer, _ count: Int) -> Bool {
        var got = 0
        while got < count {
            let n = recv(fd, buf + got, count - got, 0)
            if n <= 0 { return false }
            got += n
        }
        return true
    }

    private static func sendAll(_ fd: Int32, _ buf: UnsafeMutableRawPointer, _ count: Int) -> Bool {
        var sent = 0
        while sent < count {
            let n = Darwin.send(fd, buf + sent, count - sent, 0)
            if n <= 0 { return false }
            sent += n
        }
        return true
    }
}
