/* mock 层共用小工具：模拟网络/桥延迟（真实桥没有这个概念，不进 contract/bridge）。 */
export const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))
