// A tree's signature: one number over what a walk read, so "did anything change" is a
// comparison of two numbers and not of two trees (daemon ADR 0006 point 5, the settle). FNV-1a,
// which is the same in every process, so a signature can be logged and compared across runs.

import CoreGraphics
import Foundation

struct Signature: Equatable {
    private(set) var value: UInt64 = 0xcbf2_9ce4_8422_2325

    private mutating func mix(_ byte: UInt8) {
        value ^= UInt64(byte)
        value = value &* 0x0000_0100_0000_01b3
    }

    /// Each value ends with a byte no text holds, so ("ab", "c") and ("a", "bc") differ.
    private mutating func end() {
        mix(0xff)
    }

    mutating func add(_ text: String) {
        for byte in text.utf8 { mix(byte) }
        end()
    }

    mutating func add(_ number: Int) {
        var rest = UInt64(bitPattern: Int64(number))
        for _ in 0..<8 {
            mix(UInt8(truncatingIfNeeded: rest))
            rest >>= 8
        }
        end()
    }

    mutating func add(_ flag: Bool?) {
        mix(flag == nil ? 2 : (flag == true ? 1 : 0))
        end()
    }

    /// A frame to the half point: layout that jitters by less is not a change.
    mutating func add(_ rect: CGRect?) {
        guard let rect = rect.flatMap(finiteRect) else {
            add("-")
            return
        }
        for value in [rect.minX, rect.minY, rect.width, rect.height] {
            add(Int((value * 2).rounded()))
        }
    }
}
