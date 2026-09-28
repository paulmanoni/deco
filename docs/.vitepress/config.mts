import { defineConfig } from 'vitepress'

const repo = 'https://github.com/paulmanoni/deco'

export default defineConfig({
  title: 'deco',
  description:
    'Python-style decorators for Go, via code generation. Annotate any function or method with a doc comment and every caller flows through your decorators.',
  base: '/deco/',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  head: [['meta', { name: 'theme-color', content: '#00add8' }]],
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/guide/getting-started', activeMatch: '/guide/' },
      { text: 'Reference', link: '/reference/cli', activeMatch: '/reference/' },
      {
        text: 'Links',
        items: [
          { text: 'Changelog', link: `${repo}/blob/main/CHANGELOG.md` },
          { text: 'pkg.go.dev', link: 'https://pkg.go.dev/github.com/paulmanoni/deco' },
          { text: 'Examples', link: `${repo}/tree/main/examples` },
        ],
      },
    ],
    sidebar: {
      '/guide/': [
        {
          text: 'Introduction',
          items: [
            { text: 'What is deco?', link: '/guide/' },
            { text: 'Getting started', link: '/guide/getting-started' },
          ],
        },
        {
          text: 'Decorators',
          items: [
            { text: 'Using decorators', link: '/guide/using-decorators' },
            { text: 'Writing decorators', link: '/guide/writing-decorators' },
            { text: 'Methods', link: '/guide/methods' },
            { text: 'Performance', link: '/guide/performance' },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'CLI', link: '/reference/cli' },
            { text: 'Library API', link: '/reference/library' },
          ],
        },
      ],
    },
    socialLinks: [{ icon: 'github', link: repo }],
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © Paul Manoni',
    },
    search: { provider: 'local' },
    editLink: {
      pattern: `${repo}/edit/main/docs/:path`,
      text: 'Edit this page on GitHub',
    },
  },
})
