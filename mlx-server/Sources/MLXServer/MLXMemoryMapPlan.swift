import Foundation
import MLX
import MLXLMCommon

struct MLXMemoryMapPlan: Sendable {
    private static let gibibyte = 1_024 * 1_024 * 1_024
    private static let minimumRuntimeBytes = 1 * gibibyte
    private static let maximumCacheBytes = 64 * 1_024 * 1_024

    let physicalMemoryBytes: Int
    let reserveBytes: Int
    let runtimeLimitBytes: Int
    let cacheLimitBytes: Int
    let wiredLimitBytes: Int

    init(reserveGB: Int) throws {
        let physical = min(UInt64(Int.max), ProcessInfo.processInfo.physicalMemory)
        physicalMemoryBytes = Int(physical)

        let automaticReserve = max(2 * Self.gibibyte, physicalMemoryBytes / 10)
        let requestedReserve: Int
        if reserveGB > 0 {
            let (bytes, overflow) = reserveGB.multipliedReportingOverflow(by: Self.gibibyte)
            guard !overflow else { throw MLXMemoryMapError.invalidReserve }
            requestedReserve = bytes
        } else {
            requestedReserve = automaticReserve
        }
        guard requestedReserve >= 0,
              requestedReserve <= physicalMemoryBytes - Self.minimumRuntimeBytes
        else {
            throw MLXMemoryMapError.reserveExceedsPhysicalMemory(
                requestedGB: reserveGB,
                physicalGB: physicalMemoryBytes / Self.gibibyte
            )
        }

        reserveBytes = requestedReserve
        runtimeLimitBytes = physicalMemoryBytes - requestedReserve
        cacheLimitBytes = min(Self.maximumCacheBytes, max(0, runtimeLimitBytes / 32))
        // Metal 拒絕超過 recommendedMaxWorkingSetSize 的 wired limit，超過會直接
        // 觸發 MLX 的致命錯誤，因此固定策略也必須先夾到裝置上限。
        if let recommended = GPU.maxRecommendedWorkingSetBytes(), recommended > 0 {
            wiredLimitBytes = min(runtimeLimitBytes, recommended)
        } else {
            wiredLimitBytes = runtimeLimitBytes
        }
    }

    func applyBeforeLoading() {
        // memoryLimit 會讓 MLX 在配置量超出目標時等待已排程工作完成；
        // cacheLimit 則避免已釋放的中間緩衝區長時間留在配置器快取。
        Memory.memoryLimit = runtimeLimitBytes
        Memory.cacheLimit = cacheLimitBytes
        Memory.clearCache()
        fputs(
            "MLX MMap enabled physical=\(formatGB(physicalMemoryBytes)) reserve=\(formatGB(reserveBytes)) runtime_limit=\(formatGB(runtimeLimitBytes)) wired_limit=\(formatGB(wiredLimitBytes)) cache_limit=\(formatMB(cacheLimitBytes))\n",
            stderr
        )
    }

    func finishLoading() {
        Memory.clearCache()
        let snapshot = Memory.snapshot()
        let registry = MemoryMappedRegionRegistry.shared
        fputs(
            "MLX MMap loaded logical_active=\(formatGB(snapshot.activeMemory)) cache=\(formatMB(snapshot.cacheMemory)) mapped=\(formatGB(registry.mappedBytes())) mapped_resident=\(formatGB(registry.residentBytes())) footprint=\(formatGB(Self.processFootprintBytes()))\n",
            stderr
        )
    }

    /// 執行期記憶體守門：定期比對「行程 footprint + 映射權重常駐量」與預算。
    ///
    /// 超出預算時會清掉 MLX 自己的配置器快取——那是唯一能由行程主動釋放的部分；
    /// 映射權重的頁面屬於系統檔案快取，使用者空間無法強制逐出，只能如實回報。
    func startMemoryGuard() -> Task<Void, Never> {
        let limit = runtimeLimitBytes
        let interval = Self.guardIntervalNanoseconds
        return Task.detached(priority: .utility) {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: interval)
                if Task.isCancelled { return }
                let footprint = Self.processFootprintBytes()
                guard footprint > 0 else { continue }
                let resident = MemoryMappedRegionRegistry.shared.residentBytes()
                let total = footprint + resident
                guard total > limit else { continue }

                let excess = total - limit
                Memory.clearCache()
                let after = Self.processFootprintBytes()
                    + MemoryMappedRegionRegistry.shared.residentBytes()
                fputs(
                    "MLX MMap over_limit by=\(Self.megabytes(excess)) before=\(Self.megabytes(total)) after_cache_clear=\(Self.megabytes(after)) limit=\(Self.megabytes(limit))\n",
                    stderr
                )
            }
        }
    }

    private static let guardIntervalNanoseconds: UInt64 = 2_000_000_000

    /// 讀取本行程的 phys_footprint；映射的乾淨檔案頁面不會計入，
    /// 因此必須與 `MemoryMappedRegionRegistry.residentBytes()` 相加才是真實用量。
    static func processFootprintBytes() -> Int {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(
            MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size
        )
        let status = withUnsafeMutablePointer(to: &info) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { rebound in
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), rebound, &count)
            }
        }
        guard status == KERN_SUCCESS else { return 0 }
        return Int(info.phys_footprint)
    }

    private static func megabytes(_ bytes: Int) -> String {
        String(format: "%.1fMiB", Double(bytes) / Double(1_024 * 1_024))
    }

    func inferenceTicket() -> WiredMemoryTicket {
        WiredFixedPolicy(limit: wiredLimitBytes).ticket(size: 0, kind: .active)
    }

    private func formatGB(_ bytes: Int) -> String {
        String(format: "%.2fGiB", Double(bytes) / Double(Self.gibibyte))
    }

    private func formatMB(_ bytes: Int) -> String {
        String(format: "%.1fMiB", Double(bytes) / Double(1_024 * 1_024))
    }
}

enum MLXMemoryMapError: LocalizedError, Sendable {
    case invalidReserve
    case reserveExceedsPhysicalMemory(requestedGB: Int, physicalGB: Int)

    var errorDescription: String? {
        switch self {
        case .invalidReserve:
            "MLX MMap 記憶體保留目標無效。"
        case .reserveExceedsPhysicalMemory(let requestedGB, let physicalGB):
            "MLX MMap 記憶體保留目標（\(requestedGB) GB）超過可用的實體記憶體（\(physicalGB) GB）。"
        }
    }
}
