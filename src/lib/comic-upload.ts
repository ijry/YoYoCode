/** Read original bytes rather than File.text(): decoding belongs to the
 *  backend so a GBK novel never gets replaced with UTF-8 replacement chars. */
export function readComicUpload(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error("File read failed"))
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("File read failed"))
        return
      }
      resolve(reader.result.slice(reader.result.indexOf(",") + 1))
    }
    reader.readAsDataURL(file)
  })
}
