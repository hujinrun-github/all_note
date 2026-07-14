import MarkdownIt from 'markdown-it'

const markdown = new MarkdownIt({
  breaks: true,
  html: true,
  linkify: true,
})

const AOZORA_RUBY_PATTERN = /｜([^《\n]+)《[^》\n]+》/g
const BLOCK_SELECTOR = [
  'address',
  'article',
  'aside',
  'blockquote',
  'div',
  'figcaption',
  'footer',
  'h1',
  'h2',
  'h3',
  'h4',
  'h5',
  'h6',
  'header',
  'li',
  'main',
  'p',
  'pre',
  'section',
].join(',')

export function markdownToPlainText(value: string) {
  if (!value.trim()) return ''

  const sourceWithoutRubyReadings = value.replace(AOZORA_RUBY_PATTERN, '$1')
  const parsed = new DOMParser().parseFromString(
    markdown.render(sourceWithoutRubyReadings),
    'text/html'
  )

  parsed.body
    .querySelectorAll('script, style, template, noscript, rt')
    .forEach((element) => element.remove())
  parsed.body.querySelectorAll('img').forEach((image) => {
    image.replaceWith(parsed.createTextNode(image.getAttribute('alt') ?? ''))
  })
  parsed.body.querySelectorAll('br').forEach((lineBreak) => {
    lineBreak.replaceWith(parsed.createTextNode(' '))
  })
  parsed.body.querySelectorAll(BLOCK_SELECTOR).forEach((block) => {
    block.append(parsed.createTextNode(' '))
  })

  return (parsed.body.textContent ?? '').replace(/\s+/g, ' ').trim()
}
