import { ref } from 'vue';

const toasts = ref([]);
let nextId = 1;

export function useToast() {
  function add(message, type = 'info', duration = 3200) {
    const id = nextId++;
    toasts.value.push({ id, message, type });
    setTimeout(() => {
      remove(id);
    }, duration);
  }

  function remove(id) {
    toasts.value = toasts.value.filter(t => t.id !== id);
  }

  return {
    toasts,
    add,
    remove,
    success: (msg, dur) => add(msg, 'success', dur),
    error: (msg, dur) => add(msg, 'error', dur),
    info: (msg, dur) => add(msg, 'info', dur),
    warn: (msg, dur) => add(msg, 'warn', dur),
  };
}
