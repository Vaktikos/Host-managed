import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import de from './de.json'
import en from './en.json'

export const languages = ['de', 'en'] as const
export type Language = (typeof languages)[number]

const stored = (() => {
  try {
    return localStorage.getItem('hm.lang')
  } catch {
    return null
  }
})()

void i18n.use(initReactI18next).init({
  resources: { de: { translation: de }, en: { translation: en } },
  lng: stored === 'en' || stored === 'de' ? stored : 'de',
  fallbackLng: ['en', 'de'],
  interpolation: { escapeValue: false }, // React escapes output
  returnNull: false,
  // Never show raw keys: fall back to a readable form of the last segment.
  parseMissingKeyHandler: (key: string) => {
    const last = key.split('.').pop() ?? key
    return last.replace(/_/g, ' ')
  },
})

export function setLanguage(lang: Language) {
  void i18n.changeLanguage(lang)
  document.documentElement.lang = lang
  try {
    localStorage.setItem('hm.lang', lang)
  } catch {
    // storage unavailable
  }
}

document.documentElement.lang = i18n.language

export default i18n
