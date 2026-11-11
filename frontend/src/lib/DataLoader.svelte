<script lang="ts">
  let { label = 'Loading data' }: { label?: string } = $props();
</script>

<div class="data-loader" role="status" aria-label={label}>
  <div class="loader-bars" aria-hidden="true">
    <span></span><span></span><span></span>
  </div>
</div>

<style>
  .data-loader {
    position: absolute;
    z-index: 2;
    inset: 0;
    display: grid;
    place-items: center;
    min-height: 96px;
    background: #222526cc;
    border-radius: inherit;
    cursor: progress;
  }

  .loader-bars { display: flex; align-items: center; gap: 6px; height: 40px; }
  .loader-bars span {
    width: 5px;
    height: 28px;
    border-radius: 3px;
    background: var(--accent);
    animation: loading-wave 1.1s ease-in-out infinite;
  }
  .loader-bars span:nth-child(2) { animation-delay: .13s; }
  .loader-bars span:nth-child(3) { animation-delay: .26s; }

  @keyframes loading-wave {
    0%, 70%, 100% { transform: scaleY(.4); opacity: .45; }
    35% { transform: scaleY(1); opacity: 1; }
  }

  @media (prefers-reduced-motion: reduce) {
    .loader-bars span { animation: none; }
    .loader-bars span:first-child, .loader-bars span:last-child { transform: scaleY(.6); }
  }
</style>
