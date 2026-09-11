declare module 'crypto-js' {
  interface WordArrayLike {
    toString(encoder?: unknown): string;
  }

  const CryptoJS: {
    enc: {
      Utf8: {
        parse(value: string): WordArrayLike;
      };
    };
    DES: {
      decrypt(
        ciphertext: string,
        key: WordArrayLike,
        options: { iv: WordArrayLike; mode: unknown; padding: unknown }
      ): WordArrayLike;
    };
    mode: {
      ECB: unknown;
    };
    pad: {
      Pkcs7: unknown;
    };
  };

  export default CryptoJS;
}
