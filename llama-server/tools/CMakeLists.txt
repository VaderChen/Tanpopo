# LlamaLoader 只建置 llama-server 與其直接相依元件。

find_package(Threads REQUIRED)

llama_add_compile_flags()

add_subdirectory(ui)
add_subdirectory(mtmd)

if (LLAMA_BUILD_SERVER)
    add_subdirectory(server)
endif()
