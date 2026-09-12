package com.openspot.extensioncore

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.lang.reflect.InvocationTargetException
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class OpenSpotExtensionCoreModule : Module() {
  private val keyAlias = "openspot_extension_core_storage_v1"
  private val preferences = "openspot_extension_core"
  private val keyValue = "storage_master_key"

  override fun definition() = ModuleDefinition {
    Name("OpenSpotExtensionCore")

    Function("call") { operation: String, payload: String ->
      if (operation == "InitExtensionSystem") {
        val key = loadOrCreateStorageKey()
        invokeGo("SetExtensionStorageMasterKey", jsonArray(key))
      }
      invokeGo(operation, payload)
    }
  }

  private fun context(): Context =
    appContext.reactContext ?: error("OpenSpot Extension Core requires an Android context")

  private fun loadOrCreateStorageKey(): String {
    val prefs = context().getSharedPreferences(preferences, Context.MODE_PRIVATE)
    val encoded = prefs.getString(keyValue, null)
    if (!encoded.isNullOrBlank()) {
      val encrypted = Base64.decode(encoded, Base64.NO_WRAP)
      check(encrypted.size > 12) { "Extension Core storage key is corrupt" }
      val iv = encrypted.copyOfRange(0, 12)
      val ciphertext = encrypted.copyOfRange(12, encrypted.size)
      val plain = cipher(Cipher.DECRYPT_MODE, iv).doFinal(ciphertext)
      check(plain.size == 32) { "Extension Core storage key has invalid length" }
      return Base64.encodeToString(plain, Base64.NO_WRAP)
    }

    val plain = ByteArray(32)
    SecureRandom().nextBytes(plain)
    val encryptor = cipher(Cipher.ENCRYPT_MODE, null)
    val ciphertext = encryptor.doFinal(plain)
    val stored = ByteArray(encryptor.iv.size + ciphertext.size)
    System.arraycopy(encryptor.iv, 0, stored, 0, encryptor.iv.size)
    System.arraycopy(ciphertext, 0, stored, encryptor.iv.size, ciphertext.size)
    check(prefs.edit().putString(keyValue, Base64.encodeToString(stored, Base64.NO_WRAP)).commit()) {
      "Unable to persist Extension Core storage key"
    }
    return Base64.encodeToString(plain, Base64.NO_WRAP)
  }

  private fun secretKey(): SecretKey {
    val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    (store.getKey(keyAlias, null) as? SecretKey)?.let { return it }
    val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
    generator.init(
      KeyGenParameterSpec.Builder(
        keyAlias,
        KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
      )
        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
        .setUserAuthenticationRequired(false)
        .build(),
    )
    return generator.generateKey()
  }

  private fun cipher(mode: Int, iv: ByteArray?): Cipher {
    val cipher = Cipher.getInstance("AES/GCM/NoPadding")
    if (iv == null) {
      cipher.init(mode, secretKey())
    } else {
      cipher.init(mode, secretKey(), GCMParameterSpec(128, iv))
    }
    return cipher
  }

  private fun jsonArray(vararg values: String): String =
    values.joinToString(prefix = "[", postfix = "]") {
      "\"" + it.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
    }

  private fun invokeGo(operation: String, payload: String): String {
    val candidates = listOf("go_backend.Gobackend", "gobackend.Gobackend")
    var lastError: Throwable? = null
    for (className in candidates) {
      try {
        val type = Class.forName(className)
        val method = type.methods.firstOrNull {
          it.name == "callOpenSpotExtensionJSON" && it.parameterTypes.size == 2
        } ?: continue
        return (method.invoke(null, operation, payload) as? String) ?: "null"
      } catch (error: ClassNotFoundException) {
        lastError = error
      } catch (error: InvocationTargetException) {
        throw error.targetException
      } catch (error: Throwable) {
        lastError = error
      }
    }
    throw IllegalStateException("Gobackend AAR is not linked", lastError)
  }
}
