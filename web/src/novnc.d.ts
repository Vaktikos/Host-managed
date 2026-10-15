declare module '@novnc/novnc/lib/rfb.js' {
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { wsProtocols?: string[]; credentials?: { password?: string } })
    scaleViewport: boolean
    resizeSession: boolean
    background: string
    viewOnly: boolean
    disconnect(): void
    sendCtrlAltDel(): void
    focus(): void
  }
}
