<!-- Adapted from pure-admin/pure-admin-thin lay-content (MIT).
     Keeps the router-view + keep-alive content model; removes account-specific layout plumbing.
     Copyright (c) 2020-present pure-admin. See licenses/pure-admin.txt. -->
<template>
  <main class="app-main" id="main-content" tabindex="-1">
    <router-view v-slot="{ Component, route }"
      ><keep-alive :max="8"
        ><component :is="Component" :key="route.path" /></keep-alive
    ></router-view>
  </main>
</template>
