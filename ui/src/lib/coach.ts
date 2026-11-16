// The first-run coach of variant E: four steps, each tied to a real element on its screen.
//   1. Macros: the Record button.      2. The recorded page: the hint card (drawn by the recorder).
//   3. Review: Download Excel template. 4. Editor: the Run button.
// Elements carry a data-coach attribute. State is in memory; the "done" flag is in localStorage.
import { reactive } from 'vue'

export const COACH_KEY = 'ladilala.ux.onboarded.e'
/** Set when the user chose "never show again" on the hint card in the page. */
export const HINT_OFF_KEY = 'ladilala.ux.hint.off.e'
export const COACH_STEPS = 4

export const coach = reactive({ step: 0 })

export const coachDone = () => localStorage.getItem(COACH_KEY) === '1'
export const hintOff = () => localStorage.getItem(HINT_OFF_KEY) === '1'
export const setHintOff = (off: boolean) => { if (off) localStorage.setItem(HINT_OFF_KEY, '1'); else localStorage.removeItem(HINT_OFF_KEY) }

/** First visit: show step 1. Does nothing once the coach was finished or skipped. */
export function startCoachOnce() {
  if (!coachDone() && coach.step === 0) coach.step = 1
}
/** The Help button: replay from step 1 and bring the page hint card back. */
export function replayCoach() {
  setHintOff(false)
  coach.step = 1
}
/** Moves forward only: a screen reports that the user reached step n. */
export function reach(n: number) {
  if (coach.step > 0 && n > coach.step) coach.step = n
}
export function finishCoach() {
  coach.step = 0
  localStorage.setItem(COACH_KEY, '1')
}
