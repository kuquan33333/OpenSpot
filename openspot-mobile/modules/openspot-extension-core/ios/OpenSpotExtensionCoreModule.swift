import ExpoModulesCore
import Gobackend
import Security

public final class OpenSpotExtensionCoreModule: Module {
  private let keychainService = "com.devx.openspot.extension-core"
  private let keychainAccount = "storage-master-key-v1"

  public func definition() -> ModuleDefinition {
    Name("OpenSpotExtensionCore")

    Function("call") { (operation: String, payload: String) throws -> String in
      if operation == "InitExtensionSystem" {
        let key = try self.loadOrCreateStorageKey()
        var keyError: NSError?
        _ = GobackendCallOpenSpotExtensionJSON(
          "SetExtensionStorageMasterKey",
          "[\"\(key)\"]",
          &keyError
        )
        if let keyError { throw keyError }
      }

      var error: NSError?
      let result = GobackendCallOpenSpotExtensionJSON(operation, payload, &error)
      if let error { throw error }
      return result ?? "null"
    }
  }

  private func loadOrCreateStorageKey() throws -> String {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: keychainAccount,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var result: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    if status == errSecSuccess, let data = result as? Data {
      return data.base64EncodedString()
    }
    guard status == errSecItemNotFound else {
      throw NSError(domain: keychainService, code: Int(status), userInfo: [NSLocalizedDescriptionKey: "Unable to read Extension Core storage key"])
    }

    var bytes = [UInt8](repeating: 0, count: 32)
    guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
      throw NSError(domain: keychainService, code: -1, userInfo: [NSLocalizedDescriptionKey: "Unable to generate Extension Core storage key"])
    }
    let data = Data(bytes)
    let addQuery: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: keychainAccount,
      kSecValueData as String: data,
      kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
    ]
    let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
    guard addStatus == errSecSuccess || addStatus == errSecDuplicateItem else {
      throw NSError(domain: keychainService, code: Int(addStatus), userInfo: [NSLocalizedDescriptionKey: "Unable to persist Extension Core storage key"])
    }
    return data.base64EncodedString()
  }
}
