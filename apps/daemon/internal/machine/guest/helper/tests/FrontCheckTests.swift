import Foundation

func testAStaleFrontmostDoesNotCountWhenTheAppSaysItIsInactive() {
    // Measured in the agent: frontmost still named the accessory fixture after Finder was
    // activated over it, and the fixture's isActive said false.
    expect(!isInFront(pid: 7, isActive: false, frontNow: 7, frontAtStart: 7), "a stale frontmost")
    expect(isInFront(pid: 7, isActive: true, frontNow: 7, frontAtStart: 7), "both agree")
    expect(isInFront(pid: 7, isActive: true, frontNow: 3, frontAtStart: 3), "isActive alone")
    expect(isInFront(pid: 7, isActive: false, frontNow: 7, frontAtStart: 3), "frontmost changed to the app during the wait")
    expect(isInFront(pid: 7, isActive: nil, frontNow: 7, frontAtStart: 7), "no running application to ask")
    expect(!isInFront(pid: 7, isActive: false, frontNow: 3, frontAtStart: 3), "another app in front")
    expect(!isInFront(pid: 7, isActive: nil, frontNow: nil, frontAtStart: nil), "nothing in front")
}
