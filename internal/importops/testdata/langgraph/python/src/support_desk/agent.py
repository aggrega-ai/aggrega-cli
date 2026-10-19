# The support agent's graph. The importer never reads this file: the import
# proves it by importing the application with this file made unreadable.
from langgraph.graph import END, START, MessagesState, StateGraph


def answer(state: MessagesState) -> MessagesState:
    return {"messages": [("assistant", "Thanks, we are on it.")]}


builder = StateGraph(MessagesState)
builder.add_node("answer", answer)
builder.add_edge(START, "answer")
builder.add_edge("answer", END)
graph = builder.compile()
