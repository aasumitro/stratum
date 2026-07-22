type Flags = Record<string, boolean>
type Values = Record<string, string>

interface State {
  flags: Flags
  values: Values
}

class TransientState {
  private state: State = { flags: {}, values: {} }
  private listeners = new Set<() => void>()

  getState() {
    return this.state
  }

  subscribe(listener: () => void) {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private notify() {
    this.listeners.forEach((listener) => listener())
  }

  setFlag(key: string, value?: boolean) {
    const nextFlags = { ...this.state.flags }
    if (value === undefined) {
      delete nextFlags[key]
    } else {
      nextFlags[key] = value
    }
    this.state = { ...this.state, flags: nextFlags }
    this.notify()
  }

  setValue(key: string, value?: string) {
    const nextValues = { ...this.state.values }
    if (value === undefined) {
      delete nextValues[key]
    } else {
      nextValues[key] = value
    }
    this.state = { ...this.state, values: nextValues }
    this.notify()
  }

  reset() {
    this.state = { flags: {}, values: {} }
    this.notify()
  }
}

export const useTransientStore = new TransientState()
