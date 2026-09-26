import { ref, watchEffect } from 'vue';

const theme = ref(localStorage.getItem('bizhub-theme') || 'auto');
const isDark = ref(false);

function resolveDark() {
  if (theme.value === 'dark') return true;
  if (theme.value === 'light') return false;
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function updateDOM() {
  const dark = resolveDark();
  isDark.value = dark;
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  if (dark) {
    document.documentElement.classList.add('dark');
  } else {
    document.documentElement.classList.remove('dark');
  }
}

export function useTheme() {
  updateDOM();

  function toggle() {
    if (theme.value === 'auto') {
      theme.value = isDark.value ? 'light' : 'dark';
    } else if (theme.value === 'light') {
      theme.value = 'dark';
    } else {
      theme.value = 'light';
    }
    localStorage.setItem('bizhub-theme', theme.value);
    updateDOM();
  }

  return {
    theme,
    isDark,
    toggle,
  };
}
