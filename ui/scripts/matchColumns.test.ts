import { expect, test } from 'bun:test'
import { hintsOfStep, isPasswordStep, matchColumn, normalize, suggestForSteps, varNameOf } from '../src/lib/matchColumns'
import type { Step } from '../src/bridge/macros-types'

const cols = ['first_name', 'last_name', 'email', 'phone', 'city', 'date']

test('normalize strips case, diacritics and separators', () => {
  expect(normalize('Số điện thoại')).toBe('sodienthoai')
  expect(normalize('First-Name_')).toBe('firstname')
})
test('exact normalized match wins', () => {
  expect(matchColumn(['First name'], cols)).toMatchObject({ column: 'first_name', reason: 'exact' })
  expect(matchColumn(['E-mail'], cols)).toMatchObject({ column: 'email', reason: 'exact' })
})
test('contains match', () => {
  expect(matchColumn(['Your email address'], cols)).toMatchObject({ column: 'email', reason: 'contains' })
})
test('synonyms in English and Vietnamese', () => {
  expect(matchColumn(['Given name'], cols)?.column).toBe('first_name')
  expect(matchColumn(['Surname'], cols)?.column).toBe('last_name')
  expect(matchColumn(['Tên'], cols)?.column).toBe('first_name')
  expect(matchColumn(['Họ'], cols)?.column).toBe('last_name')
  expect(matchColumn(['Số điện thoại'], cols)?.column).toBe('phone')
  expect(matchColumn(['Mobile'], cols)?.column).toBe('phone')
  expect(matchColumn(['Thành phố'], cols)?.column).toBe('city')
  expect(matchColumn(['Ngày'], cols)?.column).toBe('date')
  expect(matchColumn(['Địa chỉ'], ['dia_chi', 'email'])?.column).toBe('dia_chi')
})
test('a short synonym is a whole word, not a substring', () => {
  expect(matchColumn(['Hotel'], cols)).toBeNull()
  expect(matchColumn(['Telephone number'], cols)?.column).toBe('phone')
})
test('no match returns null', () => {
  expect(matchColumn(['Coupon code'], cols)).toBeNull()
  expect(matchColumn([], cols)).toBeNull()
})
test('a password field never matches', () => {
  expect(matchColumn(['Password'], ['password', 'email'])).toBeNull()
  expect(matchColumn(['Mật khẩu'], ['mat_khau'])).toBeNull()
})
test('a used column ranks lower, not out', () => {
  expect(matchColumn(['Email'], ['email'], new Set(['email']))?.column).toBe('email')
  expect(matchColumn(['Mail'], ['email', 'mail_alt'], new Set(['mail_alt']))?.column).toBe('email')
})
const fill = (id: string, label: string, v: string, css?: string): Step => ({ id, action: 'fill', target: { locators: [{ kind: 'label', value: label }, ...(css ? [{ kind: 'css' as const, value: css }] : [])] }, value: { expr: v } })
test('hints come from label, role name and css attributes', () => {
  const s: Step = { id: 'a', action: 'fill', target: { locators: [{ kind: 'role', value: 'textbox', name: 'Phone' }, { kind: 'css', value: 'input[name="mobile_no"]#ph' }] }, value: { expr: '1' } }
  expect(hintsOfStep(s)).toEqual(['Phone', 'mobile_no', 'ph'])
})
test('suggestions skip mapped, password and unmatched steps', () => {
  const steps = [fill('s1', 'First name', 'Anna'), fill('s2', 'Email', '{{email}}'), fill('s3', 'Password', '{{password}}'), fill('s4', 'Coupon', 'X'), fill('s5', 'Phone', '0901')]
  expect(isPasswordStep(steps[2])).toBe(true)
  expect(suggestForSteps(steps, cols).map(s => [s.stepId, s.column])).toEqual([['s1', 'first_name'], ['s5', 'phone']])
})
test('varNameOf follows the Go rule', () => {
  expect(varNameOf('first_name')).toBe('first_name')
  expect(varNameOf('E-mail address')).toBe('E_mail_address')
  expect(varNameOf('Họ tên')).toBe('Ho_ten')
  expect(varNameOf('2nd')).toBe('c_2nd')
})
