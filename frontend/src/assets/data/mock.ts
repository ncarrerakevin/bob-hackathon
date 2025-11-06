export type Conversation = {
  id: string;
  name: string;
  avatar: string;
  lastMessage: string;
  time: string;
  unread?: number;
  active?: boolean;
}

export const conversations: Conversation[] = [
  {
    id: "1",
    name: "John Doe",
    avatar: "https://lh3.googleusercontent.com/aida-public/AB6AXuA-fA4OALi65GcmvIB0zuq9R0DvuPInBKHQJZJc6DKnOMmu07dHPSrhHSibT04u_dO8I90E14hkm1hy1orqcycT6lfmEQVG36JHtX1yROh64RDmh18UgtRDOOeI7iTcWEHe2mIpv6efml__iC94cHFM_7UuCihQ8g0HKmY4SL-T-B3zuUZJ-PNHabnmu5wChi28uBoym4K3405bSZGmPKMEw0NIcNuG_LbSizsxhbw7R7bSKcB0Zte2_P1d_yLowHyHDuQ5trA9TqGX",
    lastMessage: "Sure, I'm interested in the new SUV...",
    time: "5m ago",
    unread: 2,
    active: true
  },
  {
    id: "2",
    name: "Jane Smith",
    avatar: "https://lh3.googleusercontent.com/aida-public/AB6AXuCTq-0wyssPU0Y3aYUEZkX_DH5ht7PmRvWeq0J-aNzi5kPTnkSFr5LQdRRaLERE3M2mCijLXnOWZDyzJeHNxgmeO2gbqSzw1GsNjdN1JIUqBXV-W-58gH0moyjHiPfMrWmgBnfgGk87BOLmoSsAzPELCHncVsr_YWq6ETQs9gVUCtDQGpK7CQTsrxOZW-sq7FmjLdT9bVHBbzhsZXrDuaZZI-xrhVLLIxfE1qOxgRStH9TBhpQaF2DyHK7U7uWJLed4X1v_Rhv_X83b",
    lastMessage: "Do you have any financing options?",
    time: "32m ago",
    unread: 1
  },
  {
    id: "3",
    name: "Robert Brown",
    avatar: "https://lh3.googleusercontent.com/aida-public/AB6AXuBdVfBVeknbNVsMduk-t9f6x4tWDWoCnEIqQlQFK3JQfgaBeHaKvsPSU_Q4Tq0o8OPimDemwqybL4y2-QKsYwrfDH50oTeNL_604682BqzkAyYDJGrUlAJuYlY3dz-kGNCvpiFEbH0c8j7-J9ovvL3AkzN0HT98wUFra_44KCNZ98ERAIJZG2aNMfYk77oYiNGW1R21zJ3fu3TQRIpMmmNbeyBS4Qu_mqEeP8wG4FD6nE1ko8waYJMdA9W6UNMG2es6UA5YcC9kpzol",
    lastMessage: "Great, I'll be there tomorrow at 2 PM.",
    time: "Yesterday"
  },
  {
    id: "4",
    name: "Emily Davis",
    avatar: "https://lh3.googleusercontent.com/aida-public/AB6AXuDlRWfwrLv8GTNhXsWnrwWl_T2aAB_rCKpDsWQZmMwhZ6ph9v9Q1el23qlEVT8evPt_glNNSTn-T6SzHAk-dtWF6K3R-eNI9_ULhCavSqv9Vyb4jymvv0q5xxK2ohtwASV6XXj9slp-x14lIV16j9LX9lrD7gu4Ontey7xZrVBPYkvV7RY-ZJfzoxrAyrtQxnVuU4fqYEQAMFk9LRpi3zhRctzJ4ns6Jee8KcXrs68vUNMFIw8eCNAR9c4Q8sIoXffVAVWcdskFgNe4",
    lastMessage: "What colors does it come in?",
    time: "2 days ago"
  }
]

export type Message = {
  id: string;
  from: 'customer'|'agent'|'system';
  text: string;
  time?: string;
}

export const messages: Message[] = [
  {
    id: 'm1',
    from: 'customer',
    text: 'Hi, I saw an ad for the new 2024 Explorer. Is it available for a test drive?',
    time: '10:45 AM'
  },
  {
    id: 'm2',
    from: 'agent',
    text: 'Hello John! Absolutely, the 2024 Explorer is a fantastic choice. We have several models available. When would be a good time for you to come in?',
    time: '10:46 AM'
  },
  {
    id: 'm3',
    from: 'system',
    text: 'John Doe was assigned to Agent Smith'
  },
  {
    id: 'm4',
    from: 'customer',
    text: 'Sure, I\'m interested in the new SUV. How about this Saturday around 11 AM?',
    time: '10:51 AM'
  }
]
