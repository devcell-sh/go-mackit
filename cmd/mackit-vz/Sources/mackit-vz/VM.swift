import Foundation
import Virtualization

// VMManager handles the VZ virtual machine lifecycle.
class VMManager {
    private var vm: VZVirtualMachine?
    private var vnc: VNCServer?
    private var vncPort: Int = 0

    // MARK: - Install

    func install(req: Request) {
        guard let ipswPath = req.ipsw, let diskPath = req.disk, let outDir = req.out_dir else {
            writeError(id: req.id, "install requires ipsw, disk, out_dir")
            return
        }

        let diskSizeGB = req.disk_size_gb ?? 64
        let cpus = req.cpus ?? 4
        let memoryGB = req.memory_gb ?? 8
        log("install: ipsw=\(ipswPath) disk=\(diskPath) size=\(diskSizeGB)GB cpus=\(cpus) mem=\(memoryGB)GB out=\(outDir)")

        // Create disk if needed
        let diskURL = URL(fileURLWithPath: diskPath)
        if !FileManager.default.fileExists(atPath: diskPath) {
            log("creating disk at \(diskPath), size=\(diskSizeGB)GB")
            FileManager.default.createFile(atPath: diskPath, contents: nil)
            let fh = try! FileHandle(forWritingTo: diskURL)
            fh.truncateFile(atOffset: UInt64(diskSizeGB) * 1024 * 1024 * 1024)
            fh.closeFile()
            log("disk created")
        } else {
            let attrs = try? FileManager.default.attributesOfItem(atPath: diskPath)
            let size = (attrs?[.size] as? UInt64) ?? 0
            log("disk exists at \(diskPath), size=\(size / (1024*1024*1024))GB")
        }

        // Resolve IPSW
        let ipswURL: URL
        if ipswPath == "latest" {
            log("resolving latest IPSW URL...")
            let sem = DispatchSemaphore(value: 0)
            var resolvedURL: URL?
            var resolveError: Error?
            VZMacOSRestoreImage.fetchLatestSupported { result in
                switch result {
                case .success(let image):
                    resolvedURL = image.url
                case .failure(let err):
                    resolveError = err
                }
                sem.signal()
            }
            sem.wait()
            guard let url = resolvedURL else {
                writeError(id: req.id, "failed to resolve IPSW: \(resolveError?.localizedDescription ?? "unknown")")
                return
            }
            // Cache the IPSW locally to avoid re-downloading
            if let cached = cacheIPSW(remoteURL: url) {
                ipswURL = cached
            } else {
                ipswURL = url
            }
        } else {
            ipswURL = URL(fileURLWithPath: ipswPath)
        }

        log("loading IPSW from \(ipswURL.path)...")
        let sem = DispatchSemaphore(value: 0)
        var restoreImage: VZMacOSRestoreImage?
        var loadError: Error?
        VZMacOSRestoreImage.load(from: ipswURL) { result in
            switch result {
            case .success(let img):
                restoreImage = img
            case .failure(let err):
                loadError = err
            }
            sem.signal()
        }
        sem.wait()

        guard let image = restoreImage else {
            writeError(id: req.id, "failed to load IPSW: \(loadError?.localizedDescription ?? "unknown")")
            return
        }
        log("IPSW loaded, macOS \(image.operatingSystemVersion.majorVersion).\(image.operatingSystemVersion.minorVersion).\(image.operatingSystemVersion.patchVersion), build \(image.buildVersion)")

        guard let config = image.mostFeaturefulSupportedConfiguration else {
            writeError(id: req.id, "IPSW does not support this hardware")
            return
        }
        log("hardware model resolved, cpus=\(cpus) memory_gb=\(memoryGB)")

        let nvramURL = URL(fileURLWithPath: outDir).appendingPathComponent("nvram.bin")
        let aux: VZMacAuxiliaryStorage
        do {
            aux = try VZMacAuxiliaryStorage(
                creatingStorageAt: nvramURL,
                hardwareModel: config.hardwareModel,
                options: []
            )
        } catch {
            writeError(id: req.id, "creating NVRAM: \(error)")
            return
        }
        log("NVRAM created at \(nvramURL.path)")

        let ecid = VZMacMachineIdentifier()
        savePlatformData(outDir: outDir, ecid: ecid, hardwareModel: config.hardwareModel)
        log("platform data saved to \(outDir)")

        let vmConfig: VZVirtualMachineConfiguration
        do {
            vmConfig = try buildConfig(
                cpus: cpus, memoryGB: memoryGB, diskPath: diskPath,
                hardwareModel: config.hardwareModel, ecid: ecid, aux: aux,
                outDir: outDir
            )
        } catch {
            writeError(id: req.id, "building VM config: \(error)")
            return
        }
        log("VM configuration built, disk=\(diskPath)")

        // VZ requires main queue for VM operations.
        let installSem = DispatchSemaphore(value: 0)

        DispatchQueue.main.async {
            let machine = VZVirtualMachine(configuration: vmConfig)
            self.vm = machine
            log("VM created, starting installer...")

            let installer = VZMacOSInstaller(virtualMachine: machine, restoringFromImageAt: ipswURL)

            // Progress observer (throttled to 1 update/sec)
            var lastInstallReport = Date.distantPast
            var lastInstallDesc = ""
            let observation = installer.progress.observe(\.fractionCompleted) { progress, _ in
                let now = Date()
                guard now.timeIntervalSince(lastInstallReport) >= 1.0 else { return }
                lastInstallReport = now
                let pct = Int(progress.fractionCompleted * 100)
                let desc = progress.localizedDescription ?? ""
                let detail = progress.localizedAdditionalDescription ?? ""
                var msg = "installing macOS"
                if !detail.isEmpty {
                    msg = detail
                } else if !desc.isEmpty && desc != lastInstallDesc {
                    msg = desc
                }
                lastInstallDesc = desc
                writeMessage(.progress(pct, msg: msg))
            }

            let descObservation = installer.progress.observe(\.localizedDescription) { progress, _ in
                let desc = progress.localizedDescription ?? ""
                if !desc.isEmpty {
                    log("install phase: \(desc)")
                }
            }

            // Heartbeat: log progress every 10s even if KVO hasn't fired
            let heartbeat = DispatchSource.makeTimerSource(queue: DispatchQueue.global())
            heartbeat.schedule(deadline: .now() + 10, repeating: 10)
            heartbeat.setEventHandler {
                let pct = Int(installer.progress.fractionCompleted * 100)
                log("install heartbeat: \(pct)% (completed=\(installer.progress.completedUnitCount)/\(installer.progress.totalUnitCount))")
                writeMessage(.progress(pct, msg: "installing macOS"))
            }
            heartbeat.resume()

            log("starting install...")
            writeMessage(.progress(0, msg: "installing macOS"))
            installer.install { result in
                heartbeat.cancel()
                observation.invalidate()
                descObservation.invalidate()
                switch result {
                case .success:
                    log("install succeeded")
                    let attrs = try? FileManager.default.attributesOfItem(atPath: diskPath)
                    let size = (attrs?[.size] as? UInt64) ?? 0
                    log("installed disk size: \(size / (1024*1024*1024))GB (\(size) bytes)")
                    writeMessage(.progress(100, msg: "installing macOS"))
                    writeMessage(.response(id: req.id))
                case .failure(let err):
                    log("install failed: \(err)")
                    writeError(id: req.id, "install failed: \(err)")
                }
                installSem.signal()
            }
        }
        installSem.wait()
        log("install completed, VM state: \(vm?.state.rawValue ?? -1)")
    }

    // MARK: - Boot

    func boot(req: Request) {
        guard let diskPath = req.disk, let outDir = req.out_dir else {
            writeError(id: req.id, "boot requires disk, out_dir")
            return
        }

        let cpus = req.cpus ?? 4
        let memoryGB = req.memory_gb ?? 8

        let attrs = try? FileManager.default.attributesOfItem(atPath: diskPath)
        let diskSize = (attrs?[.size] as? UInt64) ?? 0
        log("boot: disk=\(diskPath) size=\(diskSize/(1024*1024*1024))GB cpus=\(cpus) mem=\(memoryGB)GB")

        log("loading platform data from \(outDir)...")
        guard let (ecid, hwModel) = loadPlatformData(outDir: outDir) else {
            writeError(id: req.id, "failed to load platform data from \(outDir)")
            return
        }
        log("platform data loaded, cpus=\(cpus) memory_gb=\(memoryGB)")

        let nvramURL = URL(fileURLWithPath: outDir).appendingPathComponent("nvram.bin")
        let aux = VZMacAuxiliaryStorage(contentsOf: nvramURL)

        let vmConfig: VZVirtualMachineConfiguration
        do {
            vmConfig = try buildConfig(
                cpus: cpus, memoryGB: memoryGB, diskPath: diskPath,
                hardwareModel: hwModel, ecid: ecid, aux: aux,
                outDir: outDir
            )
        } catch {
            writeError(id: req.id, "building VM config: \(error)")
            return
        }
        log("VM configuration built, disk=\(diskPath)")

        // VZ requires main queue for VM operations.
        let startSem = DispatchSemaphore(value: 0)

        // Build start options with guest provisioning if requested.
        var provisioningUser: String? = nil
        if let user = req.guest_user, !user.isEmpty {
            provisioningUser = user
            log("guest provisioning requested: user=\(user) ssh=\(req.enable_ssh == true)")
        }

        let macAddr = vmConfig.networkDevices.first?.macAddress.string
        log("guest MAC address: \(macAddr ?? "none")")

        DispatchQueue.main.async {
            let machine = VZVirtualMachine(configuration: vmConfig)
            self.vm = machine
            log("starting VM...")

            self.startVM(machine: machine, req: req, provisioningUser: provisioningUser, macAddress: macAddr) {
                startSem.signal()
            }
        }
        startSem.wait()
    }

    private func startVM(machine: VZVirtualMachine, req: Request, provisioningUser: String?, macAddress: String?, completion: @escaping () -> Void) {
        #if compiler(>=6.4)
        if let user = provisioningUser, #available(macOS 27, *) {
            Task { @MainActor in
                do {
                    let startOptions = VZMacOSVirtualMachineStartOptions()
                    let provOpts = VZMacGuestProvisioningOptions()
                    provOpts.fullName = user
                    provOpts.username = user
                    if let pw = req.guest_password {
                        provOpts.password = pw
                    }
                    if req.enable_ssh == true {
                        provOpts.enablesRemoteLogin = true
                    }
                    provOpts.logsInAutomatically = true
                    try startOptions.setGuestProvisioning(provOpts)
                    log("guest provisioning set on start options")

                    try await machine.start(options: startOptions)
                    log("VM started with provisioning")
                    var resp = Message.response(id: req.id, ok: true)
                    resp.mac_address = macAddress
                    writeMessage(resp)
                } catch {
                    log("boot with provisioning failed: \(error)")
                    writeError(id: req.id, "boot failed: \(error)")
                }
                // VNC setup on the GCD main queue (not @MainActor executor).
                // _VZVNCServer may need the actual GCD main thread to bind.
                DispatchQueue.main.async {
                    log("VNC setup dispatched to main queue, thread=\(Thread.isMainThread ? "main" : "background")")
                    self.setupVNC(machine: machine, macAddress: macAddress)
                    completion()
                }
            }
            return
        }
        #endif

        machine.start { result in
            switch result {
            case .success:
                log("VM started successfully")
                var resp = Message.response(id: req.id, ok: true)
                resp.mac_address = macAddress
                writeMessage(resp)
            case .failure(let err):
                writeError(id: req.id, "boot failed: \(err)")
            }
            DispatchQueue.main.async {
                self.setupVNC(machine: machine, macAddress: macAddress)
                completion()
            }
        }
    }

    private func setupVNC(machine: VZVirtualMachine, macAddress: String?) {
        var port = 0
        if NSClassFromString("_VZVNCServer") != nil {
            log("setting up VNC... thread=\(Thread.isMainThread ? "main" : "background")")
            if let vncServer = VNCServer(virtualMachine: machine) {
                self.vnc = vncServer
                if let p = vncServer.waitForPort() {
                    port = p
                    self.vncPort = p
                    log("VNC on port \(p)")
                } else {
                    log("VNC: port not available")
                }
            } else {
                log("VNC: failed to create server (non-fatal)")
            }
        } else {
            log("VNC: _VZVNCServer not available on this OS")
        }
        writeMessage(.booted(vncPort: port, macAddress: macAddress))
        log("VM booted (vnc_port=\(port))")
    }

    // MARK: - Screenshot

    func screenshot(req: Request) {
        guard let vncServer = vnc else {
            writeError(id: req.id, "screenshot: VNC not available")
            return
        }
        guard let pngData = vncServer.captureScreenshot() else {
            writeError(id: req.id, "screenshot: capture failed")
            return
        }
        let b64 = pngData.base64EncodedString()
        writeMessage(.screenshot(id: req.id, width: 0, height: 0, pngBase64: b64))
    }

    // MARK: - Input

    func sendKeys(req: Request) {
        log("sendKeys: \(req.keys ?? []), vnc=\(vnc != nil)")
        // TODO: send key events via VNC
        writeMessage(.response(id: req.id))
    }

    func click(req: Request) {
        log("click: x=\(req.x ?? 0) y=\(req.y ?? 0), vnc=\(vnc != nil)")
        // TODO: send pointer events via VNC
        writeMessage(.response(id: req.id))
    }

    func typeText(req: Request) {
        log("typeText: \"\(req.text ?? "")\", vnc=\(vnc != nil)")
        // TODO: send text via VNC key events
        writeMessage(.response(id: req.id))
    }

    // MARK: - Stop

    func stop(req: Request) {
        log("stop requested")
        if vnc != nil {
            log("stopping VNC server")
            vnc?.stop()
            vnc = nil
            log("VNC server stopped")
        }

        if let machine = vm {
            log("requesting VM stop")
            do {
                try machine.requestStop()
                log("VM stop requested")
            } catch {
                log("stop error: \(error)")
            }
            vm = nil
        }

        writeMessage(.response(id: req.id))
        writeMessage(.stopped())
    }

    // MARK: - Config

    private func buildConfig(
        cpus: Int, memoryGB: Int, diskPath: String,
        hardwareModel: VZMacHardwareModel, ecid: VZMacMachineIdentifier,
        aux: VZMacAuxiliaryStorage, outDir: String
    ) throws -> VZVirtualMachineConfiguration {
        let config = VZVirtualMachineConfiguration()
        config.cpuCount = cpus
        config.memorySize = UInt64(memoryGB) * 1024 * 1024 * 1024

        let bootLoader = VZMacOSBootLoader()
        config.bootLoader = bootLoader

        let platform = VZMacPlatformConfiguration()
        platform.hardwareModel = hardwareModel
        platform.machineIdentifier = ecid
        platform.auxiliaryStorage = aux
        config.platform = platform

        let disk = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: diskPath), readOnly: false)
        config.storageDevices = [VZVirtioBlockDeviceConfiguration(attachment: disk)]

        let network = VZNATNetworkDeviceAttachment()
        let netConfig = VZVirtioNetworkDeviceConfiguration()
        netConfig.attachment = network
        config.networkDevices = [netConfig]

        let graphics = VZMacGraphicsDeviceConfiguration()
        graphics.displays = [VZMacGraphicsDisplayConfiguration(
            widthInPixels: 1920, heightInPixels: 1080, pixelsPerInch: 144
        )]
        config.graphicsDevices = [graphics]

        config.keyboards = [VZMacKeyboardConfiguration()]
        config.pointingDevices = [VZMacTrackpadConfiguration()]
        config.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]

        // Serial console
        let serialLogURL = URL(fileURLWithPath: outDir).appendingPathComponent("serial.log")
        FileManager.default.createFile(atPath: serialLogURL.path, contents: nil)
        let serialOut = try FileHandle(forWritingTo: serialLogURL)
        let serialIn = FileHandle(forReadingAtPath: "/dev/null")!
        let serialAttachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: serialIn, fileHandleForWriting: serialOut
        )
        let serialPort = VZVirtioConsoleDeviceSerialPortConfiguration()
        serialPort.attachment = serialAttachment
        config.serialPorts = [serialPort]

        log("config: cpus=\(cpus) mem=\(memoryGB)GB display=1920x1080 serial=\(outDir)/serial.log")
        do {
            try config.validate()
            log("config validation passed")
        } catch {
            log("config validation failed: \(error)")
            throw error
        }
        return config
    }

    // MARK: - Platform Persistence

    private func savePlatformData(outDir: String, ecid: VZMacMachineIdentifier, hardwareModel: VZMacHardwareModel) {
        let data: [String: Any] = [
            "ecid": ecid.dataRepresentation.base64EncodedString(),
            "hardware_model": hardwareModel.dataRepresentation.base64EncodedString(),
        ]
        if let json = try? JSONSerialization.data(withJSONObject: data),
           let str = String(data: json, encoding: .utf8) {
            let path = URL(fileURLWithPath: outDir).appendingPathComponent("platform.json")
            try? str.write(to: path, atomically: true, encoding: .utf8)
        }
    }

    private func loadPlatformData(outDir: String) -> (VZMacMachineIdentifier, VZMacHardwareModel)? {
        let path = URL(fileURLWithPath: outDir).appendingPathComponent("platform.json")
        guard let jsonData = try? Data(contentsOf: path),
              let dict = try? JSONSerialization.jsonObject(with: jsonData) as? [String: String],
              let ecidB64 = dict["ecid"],
              let hwB64 = dict["hardware_model"],
              let ecidData = Data(base64Encoded: ecidB64),
              let hwData = Data(base64Encoded: hwB64) else {
            return nil
        }

        guard let ecid = VZMacMachineIdentifier(dataRepresentation: ecidData),
              let hw = VZMacHardwareModel(dataRepresentation: hwData) else {
            return nil
        }
        return (ecid, hw)
    }
}

// cacheIPSW downloads a remote IPSW to ~/.cache/mackit/ipsw/ on first use
// and returns the local file URL on subsequent runs.
func cacheIPSW(remoteURL: URL) -> URL? {
    let cacheBase: URL
    if let xdg = ProcessInfo.processInfo.environment["MACKIT_CACHE_DIR"] {
        cacheBase = URL(fileURLWithPath: xdg)
    } else {
        cacheBase = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".cache/mackit")
    }
    let ipswDir = cacheBase.appendingPathComponent("ipsw")
    try? FileManager.default.createDirectory(at: ipswDir, withIntermediateDirectories: true)

    let filename = remoteURL.lastPathComponent
    let cachedPath = ipswDir.appendingPathComponent(filename)

    if FileManager.default.fileExists(atPath: cachedPath.path) {
        log("using cached IPSW: \(cachedPath.path)")
        return cachedPath
    }

    log("downloading IPSW to cache: \(cachedPath.path) ...")
    writeMessage(.progress(0, msg: "downloading IPSW"))

    let sem = DispatchSemaphore(value: 0)
    var downloadedURL: URL?
    var downloadError: Error?

    let task = URLSession.shared.downloadTask(with: remoteURL) { tempURL, response, error in
        if let error = error {
            downloadError = error
        } else if let tempURL = tempURL {
            downloadedURL = tempURL
        }
        sem.signal()
    }

    var lastDownloadReport = Date.distantPast
    let observation = task.progress.observe(\.fractionCompleted) { progress, _ in
        let now = Date()
        guard now.timeIntervalSince(lastDownloadReport) >= 1.0 else { return }
        lastDownloadReport = now
        let pct = Int(progress.fractionCompleted * 100)
        writeMessage(.progress(pct, msg: "downloading IPSW"))
    }

    task.resume()
    sem.wait()
    observation.invalidate()

    guard let tempURL = downloadedURL else {
        log("IPSW download failed: \(downloadError?.localizedDescription ?? "unknown")")
        return nil
    }

    do {
        try FileManager.default.moveItem(at: tempURL, to: cachedPath)
        log("IPSW cached: \(cachedPath.path)")
        writeMessage(.progress(100, msg: "downloading IPSW"))
        return cachedPath
    } catch {
        log("failed to cache IPSW: \(error)")
        return nil
    }
}
