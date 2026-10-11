// Where a guide topic lives in the app. A topic is written "id" or "id#anchor".
export function helpPath(topic: string): string {
  return topic === '' ? '/help' : `/help/${topic}`
}
